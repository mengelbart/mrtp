//go:build cgo

package webrtc

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mengelbart/scream-go"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func EnableSCReAM(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		t.scream = NewScreamInterceptorFactory(initRate, minRate, maxRate)
		t.interceptorRegistry.Add(t.scream)
		return nil
	}
}

var _ interceptor.Interceptor = (*ScreamInterceptor)(nil)

type newStream struct {
	ssrc            uint32
	priority        float64
	min, max, start float64
}

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
	lock         sync.Mutex
	initRate     int
	minRate      int
	maxRate      int
	interceptors map[string]*ScreamInterceptor
}

func NewScreamInterceptorFactory(initRate, minRate, maxRate int) *ScreamInterceptorFactory {
	return &ScreamInterceptorFactory{
		lock:         sync.Mutex{},
		initRate:     initRate,
		minRate:      minRate,
		maxRate:      maxRate,
		interceptors: map[string]*ScreamInterceptor{},
	}
}

func (f *ScreamInterceptorFactory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	interceptor := &ScreamInterceptor{
		NoOp:           interceptor.NoOp{},
		id:             id,
		logger:         slog.Default(),
		init:           f.initRate,
		min:            f.minRate,
		max:            f.maxRate,
		tx:             scream.NewTx(),
		txQueue:        make(chan *txPacket),
		streams:        map[uint32]*scream.Queue[*txPacket]{},
		newStreamQueue: make(chan *newStream),
		rtcpRxQueue:    make(chan *rxPacket),
		removeStream:   make(chan uint32),
		closed:         make(chan struct{}),
		onClose:        f.remove,
		wg:             sync.WaitGroup{},
	}
	f.interceptors[id] = interceptor

	interceptor.wg.Add(1)
	go func() {
		defer interceptor.wg.Done()
		interceptor.loop()
	}()
	return interceptor, nil
}

func (f *ScreamInterceptorFactory) GetTargetRate(id string, ssrc uint32) (float64, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	i, ok := f.interceptors[id]
	if !ok {
		return 0, fmt.Errorf("unknown id passed to scream interceptor factory: %s", id)
	}
	return i.getTargetBitrate(ssrc), nil
}

func (f *ScreamInterceptorFactory) remove(id string) {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.interceptors, id)
}

type ScreamInterceptor struct {
	interceptor.NoOp
	id             string
	logger         *slog.Logger
	init, min, max int
	// txMu guards all access to tx, since the underlying scream.Tx wraps a
	// non-thread-safe C struct and is otherwise only meant to be touched from
	// the loop goroutine; getTargetBitrate is called from other goroutines
	// via ScreamInterceptorFactory.GetTargetRate.
	txMu           sync.Mutex
	tx             *scream.Tx
	streams        map[uint32]*scream.Queue[*txPacket]
	newStreamQueue chan *newStream
	txQueue        chan *txPacket
	rtcpRxQueue    chan *rxPacket
	removeStream   chan uint32
	closed         chan struct{}
	onClose        func(string)
	wg             sync.WaitGroup
}

func (s *ScreamInterceptor) getTargetBitrate(ssrc uint32) float64 {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	return s.tx.GetTargetBitrate(time.Now(), ssrc)
}

func (s *ScreamInterceptor) loop() {
	timer := time.NewTimer(time.Second)
	for {
		select {
		case ns := <-s.newStreamQueue:
			if _, ok := s.streams[ns.ssrc]; ok {
				s.logger.Warn("duplicate SSRC, dropping stream", "ssrc", ns.ssrc)
				continue
			}
			s.streams[ns.ssrc] = scream.NewQueue[*txPacket]()
			s.txMu.Lock()
			s.tx.RegisterNewStream(s.streams[ns.ssrc], ns.ssrc, ns.priority, ns.min, ns.start, ns.max)
			s.txMu.Unlock()
		case pkt := <-s.txQueue:
			stream, ok := s.streams[pkt.pkt.SSRC]
			if !ok {
				s.logger.Error("got packet for unknown ssrc", "ssrc", pkt.pkt.SSRC)
				continue
			}
			stream.Enqueue(pkt)
			s.txMu.Lock()
			s.tx.NewMediaFrame(pkt.ts, pkt.pkt.SSRC, pkt.Size(), pkt.pkt.Marker)
			s.txMu.Unlock()
		case pkt := <-s.rtcpRxQueue:
			s.receiveFeedback(pkt)
			s.txMu.Lock()
			stats := s.tx.GetStatistics(time.Now())
			s.txMu.Unlock()
			s.logger.Info("got scream statistics", "stats", stats)
		case ssrc := <-s.removeStream:
			delete(s.streams, ssrc)
		case <-timer.C:
		case <-s.closed:
			return
		}
		now := time.Now()
		next := s.transmit(now)
		until := time.Until(next)
		timer.Reset(until)
	}
}

func (s *ScreamInterceptor) receiveFeedback(pkt *rxPacket) {
	pkts, err := pkt.attr.GetRTCPPackets(pkt.raw)
	if err != nil {
		s.logger.Error("failed to unmarshal RTCP packet", "error", err)
		return
	}
	for _, rtcpPkt := range pkts {
		fb, ok := rtcpPkt.(*rtcp.CCFeedbackReport)
		if !ok {
			continue
		}
		raw, err := fb.Marshal()
		if err != nil {
			s.logger.Error("failed to marshal CCFeedbackReport", "error", err)
			continue
		}
		s.txMu.Lock()
		s.tx.IncomingStandardizedFeedback(time.Now(), raw)
		s.txMu.Unlock()
	}
}

func (s *ScreamInterceptor) transmit(now time.Time) time.Time {
	var next time.Time
	for ssrc, stream := range s.streams {
		for {
			s.txMu.Lock()
			tx := s.tx.IsOkToTransmit(now, ssrc)
			s.txMu.Unlock()
			if tx == -1 {
				break
			}
			if tx == 0 {
				pkt, ok := stream.Dequeue()
				if !ok {
					break
				}
				n, err := pkt.writer.Write(&pkt.pkt.Header, pkt.pkt.Payload, pkt.attr)
				if err != nil {
					s.logger.Error("failed to write RTP packet", "err", err)
				}
				// TODO: This check fails, why?
				// if n != pkt.pkt.MarshalSize() {
				// 	s.logger.Warn("wrote incorrect size of RTP packet", "expected", pkt.pkt.MarshalSize(), "got", n)
				// }
				s.txMu.Lock()
				nextTx := s.tx.AddTransmitted(now, ssrc, n, pkt.SequenceNumber(), pkt.pkt.Marker)
				s.txMu.Unlock()
				if nextTx > 0 {
					wakeAt := now.Add(time.Duration(nextTx * float64(time.Second)))
					if next.IsZero() || wakeAt.Before(next) {
						next = wakeAt
					}
					break
				}
			}
			if tx > 0 {
				n := now.Add(time.Duration(tx * float64(time.Second)))
				if next.IsZero() || n.Before(next) {
					next = n
				}
				break
			}
		}
	}
	if next.IsZero() {
		next = now
	}
	return next
}

// BindLocalStream implements interceptor.Interceptor.
func (s *ScreamInterceptor) BindLocalStream(info *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	s.logger.Debug("binding interceptor", "info", fmt.Sprintf("%v", info))
	ns := &newStream{
		ssrc:     info.SSRC,
		priority: 0,
		min:      float64(s.min),
		max:      float64(s.max),
		start:    float64(s.init),
	}
	select {
	case s.newStreamQueue <- ns:
	case <-s.closed:
		return nil
	}
	return interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
		if attributes == nil {
			attributes = make(interceptor.Attributes)
		}
		payloadCopy := make([]byte, len(payload))
		n := copy(payloadCopy, payload)
		if n != len(payload) {
			return n, errors.New("failed to copy payload")
		}
		pkt := &rtp.Packet{Header: header.Clone(), Payload: payloadCopy}
		now := time.Now()
		select {
		case s.txQueue <- &txPacket{
			pkt:    pkt,
			attr:   attributes,
			ts:     now,
			writer: writer,
		}:
		case <-s.closed:
		}
		return pkt.MarshalSize(), nil
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
		rtcpCopy := make([]byte, n)
		m := copy(rtcpCopy, b)
		if n != m {
			return n, attr, errors.New("failed to copy RTCP packet")
		}
		select {
		case s.rtcpRxQueue <- &rxPacket{
			raw:  rtcpCopy,
			attr: attr,
		}:
		case <-s.closed:
		}
		return n, attr, nil
	})
}

// Close implements interceptor.Interceptor.
func (s *ScreamInterceptor) Close() error {
	close(s.closed)
	s.wg.Wait()
	if s.onClose != nil {
		s.onClose(s.id)
	}
	return nil
}

// UnbindLocalStream implements interceptor.Interceptor.
func (s *ScreamInterceptor) UnbindLocalStream(info *interceptor.StreamInfo) {
	select {
	case s.removeStream <- info.SSRC:
	case <-s.closed:
	}
}
