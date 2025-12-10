package webrtc

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mengelbart/scream-go"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

var _ interceptor.Interceptor = (*ScreamInterceptor)(nil)

var _ scream.Packet = (*txPacket)(nil)

type txPacket struct {
	pkt    *rtp.Packet
	attr   interceptor.Attributes
	ts     time.Time
	writer interceptor.RTPWriter
}

// SequenceNumber implements scream.Packet.
func (t *txPacket) SequenceNumber() uint16 {
	return t.pkt.SequenceNumber
}

// Size implements scream.Packet.
func (t *txPacket) Size() int {
	return t.pkt.MarshalSize()
}

// Timestamp implements scream.Packet.
func (t *txPacket) Timestamp() time.Time {
	return t.ts
}

type rxPacket struct {
	raw  []byte
	attr interceptor.Attributes
}

type ScreamInterceptorFactory struct {
	initRate int
	minRate  int
	maxRate  int

	lock         sync.Mutex
	interceptors map[string]*ScreamInterceptor
}

func NewScreamInterceptor(initRate, minRate, maxRate int) *ScreamInterceptorFactory {
	return &ScreamInterceptorFactory{
		initRate:     initRate,
		minRate:      minRate,
		maxRate:      maxRate,
		lock:         sync.Mutex{},
		interceptors: map[string]*ScreamInterceptor{},
	}
}

func (f *ScreamInterceptorFactory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	interceptor := &ScreamInterceptor{
		NoOp:        interceptor.NoOp{},
		logger:      slog.Default(),
		init:        f.initRate,
		min:         f.maxRate,
		max:         f.maxRate,
		lock:        sync.Mutex{},
		tx:          scream.NewTx(),
		txQueue:     make(chan *txPacket),
		streams:     map[uint32]*scream.Queue[*txPacket]{},
		rtcpRxQueue: make(chan *rxPacket),
		closed:      make(chan struct{}),
		wg:          sync.WaitGroup{},
		onClose:     f.remove,
	}

	f.interceptors[id] = interceptor

	interceptor.wg.Go(interceptor.loop)
	return interceptor, nil
}

func (f *ScreamInterceptorFactory) getRate(id string, ssrc uint32) (float64, bool) {
	f.lock.Lock()
	defer f.lock.Unlock()
	i, ok := f.interceptors[id]
	if !ok {
		return 0, false
	}
	return i.getRate(ssrc), true
}

func (f *ScreamInterceptorFactory) remove(id string) {
	f.lock.Lock()
	defer f.lock.Unlock()
	if i, ok := f.interceptors[id]; ok {
		i.Close()
	}
	delete(f.interceptors, id)
}

type ScreamInterceptor struct {
	interceptor.NoOp
	logger         *slog.Logger
	init, min, max int
	target         atomic.Int64 // sum over all ssrcs
	lock           sync.Mutex
	tx             *scream.Tx
	streams        map[uint32]*scream.Queue[*txPacket]
	txQueue        chan *txPacket
	rtcpRxQueue    chan *rxPacket
	closed         chan struct{}
	wg             sync.WaitGroup
	onClose        func(string)
}

func (s *ScreamInterceptor) getRate(ssrc uint32) float64 {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.tx.GetTargetBitrate(time.Now(), ssrc)
}

func (s *ScreamInterceptor) loop() {
	for {
		if !s.schedule() {
			return
		}
	}
}

func (s *ScreamInterceptor) transmit(now time.Time, ssrc uint32, stream *scream.Queue[*txPacket]) (float64, error) {
	pkt, ok := stream.Dequeue()
	if !ok {
		return 0, errors.New("stream queue empty")
	}
	n, err := pkt.writer.Write(&pkt.pkt.Header, pkt.pkt.Payload, pkt.attr)
	if err != nil {
		return 0, err
	}
	s.logger.Debug("transmitted packet", "ssrc", ssrc, "seq-nr", pkt.SequenceNumber())
	nextTx := s.tx.AddTransmitted(now, ssrc, n, pkt.SequenceNumber(), pkt.pkt.Marker)
	return nextTx, nil
}

func (s *ScreamInterceptor) schedule() bool {
	s.lock.Lock()

	now := time.Now()
	stats := s.tx.GetStatistics(now)
	s.logger.Debug("got scream statistics", "stats", stats)

	var next float64
	for ssrc, stream := range s.streams {
		var streamNext float64
		for {
			streamNext = s.tx.IsOkToTransmit(now, ssrc)
			s.logger.Debug("is ok to transmit", "now", now, "ssrc", ssrc, "val", streamNext)
			if streamNext != 0 {
				break
			}
			var err error
			streamNext, err = s.transmit(now, ssrc, stream)
			if err != nil {
				s.logger.Error("failed to transmit packet", "err", err)
				break
			}
		}
		if streamNext > 0 && streamNext < next {
			next = streamNext
		}
	}
	s.lock.Unlock()

	s.logger.Debug("scheduler waiting")

	var timer <-chan time.Time
	if next > 0 {
		timer = time.After(time.Duration(next) * time.Second)
	}

	select {
	case <-timer:
		return true
	case pkt := <-s.txQueue:
		s.enqueuePacket(pkt)
		return true
	case pkt := <-s.rtcpRxQueue:
		s.receiveFeedback(pkt)
		return true
	case <-s.closed:
		return false
	}
}

func (s *ScreamInterceptor) enqueuePacket(pkt *txPacket) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.logger.Debug("enqueuing packet", "ssrc", pkt.pkt.SSRC)
	stream, ok := s.streams[pkt.pkt.SSRC]
	if !ok {
		s.logger.Error("got packet for unknown ssrc", "ssrc", pkt.pkt.SSRC)
		return
	}
	stream.Enqueue(pkt)
	s.tx.NewMediaFrame(pkt.ts, pkt.pkt.SSRC, pkt.Size(), pkt.pkt.Marker)
}

func (s *ScreamInterceptor) receiveFeedback(pkt *rxPacket) {
	s.lock.Lock()
	defer s.lock.Unlock()

	pkts, err := pkt.attr.GetRTCPPackets(pkt.raw)
	if err != nil {
		s.logger.Error("failed to unmarshal RTCP packet", "error", err)
		return
	}
	for _, rtcpPkt := range pkts {
		_, ok := rtcpPkt.(*rtcp.CCFeedbackReport)
		if ok {
			s.tx.IncomingStandardizedFeedback(time.Now(), pkt.raw)
		}
	}
}

// BindLocalStream implements interceptor.Interceptor.
func (s *ScreamInterceptor) BindLocalStream(info *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	s.logger.Debug("binding interceptor", "info", info)
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.streams[info.SSRC]; ok {
		s.logger.Warn("duplicate SSRC, overwriting previous stream", "ssrc", info.SSRC)
	}
	q := scream.NewQueue[*txPacket]()
	s.tx.RegisterNewStream(q, info.SSRC, 0, float64(s.min), float64(s.init), float64(s.max))
	s.streams[info.SSRC] = q

	return interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
		if attributes == nil {
			attributes = make(interceptor.Attributes)
		}
		tp := &txPacket{
			pkt: &rtp.Packet{
				Header:  header.Clone(),
				Payload: make([]byte, len(payload)),
			},
			attr:   attributes,
			ts:     time.Now(),
			writer: writer,
		}
		n := copy(tp.pkt.Payload, payload)
		if n != len(payload) {
			return n, errors.New("failed to copy payload")
		}
		select {
		case s.txQueue <- tp:
		case <-s.closed:
			return 0, errors.New("interceptor closed")
		}
		return tp.pkt.MarshalSize(), nil
	})
}

// BindRTCPReader implements interceptor.Interceptor.
func (s *ScreamInterceptor) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return interceptor.RTCPReaderFunc(func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
		n, attr, err := reader.Read(b, a)
		if err != nil {
			return n, attr, err
		}
		if attr == nil {
			attr = make(interceptor.Attributes)
		}
		rp := &rxPacket{
			raw:  make([]byte, n),
			attr: attr,
		}
		m := copy(rp.raw, b)
		if n != m {
			return n, attr, errors.New("failed to copy RTCP packet")
		}
		select {
		case s.rtcpRxQueue <- rp:
		case <-s.closed:
			return 0, attr, errors.New("interceptor closed")
		}
		return n, attr, nil
	})
}

// Close implements interceptor.Interceptor.
func (s *ScreamInterceptor) Close() error {
	close(s.closed)
	s.wg.Wait()
	return nil
}

// UnbindLocalStream implements interceptor.Interceptor.
func (s *ScreamInterceptor) UnbindLocalStream(info *interceptor.StreamInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.streams, info.SSRC)
}
