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

func NewScreaminterceptorFactory(initRate, minRate, maxRate int) *ScreamInterceptorFactory {
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
		logger:         slog.Default(),
		init:           f.initRate,
		min:            f.maxRate,
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
		defer interceptor.wg.Wait()
		interceptor.loop()
	}()
	return interceptor, nil
}

func (f *ScreamInterceptorFactory) GetTargetRate(id string, ssrc uint32) (float64, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	i, ok := f.interceptors[id]
	if !ok {
		return 0, errors.New("unknown id")
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
	logger         *slog.Logger
	init, min, max int
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
	return s.tx.GetTargetBitrate(time.Now(), ssrc)
}

func (s *ScreamInterceptor) loop() {
	timer := time.NewTimer(time.Second)
	for {
		stats := s.tx.GetStatistics(time.Now())
		s.logger.Debug("got scream statistics", "stats", stats)
		select {
		case ns := <-s.newStreamQueue:
			if _, ok := s.streams[ns.ssrc]; ok {
				s.logger.Warn("duplicate SSRC, dropping stream", "ssrc", ns.ssrc)
				continue
			}
			s.streams[ns.ssrc] = scream.NewQueue[*txPacket]()
			s.tx.RegisterNewStream(s.streams[ns.ssrc], ns.ssrc, ns.priority, ns.min, ns.start, ns.max)
		case pkt := <-s.txQueue:
			stream, ok := s.streams[pkt.pkt.SSRC]
			if !ok {
				s.logger.Error("got packet for unknown ssrc", "ssrc", pkt.pkt.SSRC)
			}
			stream.Enqueue(pkt)
			s.tx.NewMediaFrame(pkt.ts, pkt.pkt.SSRC, pkt.Size(), pkt.pkt.Marker)
		case pkt := <-s.rtcpRxQueue:
			s.receiveFeedback(pkt)
		case <-timer.C:
		case <-s.closed:
			return
		}
		now := time.Now()
		next := s.transmit(now)
		timer.Reset(time.Until(next))
	}
}

func (s *ScreamInterceptor) receiveFeedback(pkt *rxPacket) {
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

func (s *ScreamInterceptor) transmit(now time.Time) time.Time {
	next := now.Add(time.Second)
	for ssrc, stream := range s.streams {
		for {
			tx := s.tx.IsOkToTransmit(time.Now(), ssrc)
			if tx < 0 {
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
				nextTx := s.tx.AddTransmitted(now, ssrc, n, pkt.SequenceNumber(), pkt.pkt.Marker)
				if nextTx > 0 {
					break
				}
			} else {
				n := now.Add(time.Duration(tx) * time.Second)
				if next.IsZero() || n.After(next) {
					next = n
				}
				break
			}
		}
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
		s.txQueue <- &txPacket{
			pkt:    pkt,
			attr:   attributes,
			ts:     now,
			writer: writer,
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
		s.rtcpRxQueue <- &rxPacket{
			raw:  rtcpCopy,
			attr: attr,
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
	select {
	case s.removeStream <- info.SSRC:
	case <-s.closed:
	}
}
