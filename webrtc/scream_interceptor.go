//go:build cgo

package webrtc

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/mengelbart/scream-go"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func EnableSCReAM(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		if minRate <= 0 {
			return fmt.Errorf("invalid SCReAM min rate: %v, must be positive", minRate)
		}
		if maxRate < minRate {
			return fmt.Errorf("invalid SCReAM max rate: %v, must be at least the min rate %v", maxRate, minRate)
		}
		if initRate < minRate || initRate > maxRate {
			return fmt.Errorf("invalid SCReAM init rate: %v, must be within [%v, %v]", initRate, minRate, maxRate)
		}
		t.registerCCFB()
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

// screamStream tracks a stream registered with SCReAM. The registration cannot
// be undone, so entries persist after unbind with bound set to false.
type screamStream struct {
	queue *scream.Queue[*txPacket]
	bound bool
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
		streams:        map[uint32]*screamStream{},
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
	txClosed       bool
	streams        map[uint32]*screamStream
	newStreamQueue chan *newStream
	txQueue        chan *txPacket
	rtcpRxQueue    chan *rxPacket
	removeStream   chan uint32
	closed         chan struct{}
	closeOnce      sync.Once
	onClose        func(string)
	wg             sync.WaitGroup
}

func (s *ScreamInterceptor) getTargetBitrate(ssrc uint32) float64 {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	if s.txClosed {
		return 0
	}
	return s.tx.GetTargetBitrate(time.Now(), ssrc)
}

func (s *ScreamInterceptor) loop() {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	var lastStats time.Time
	for {
		select {
		case ns := <-s.newStreamQueue:
			if st, ok := s.streams[ns.ssrc]; ok {
				if st.bound {
					s.logger.Warn("duplicate SSRC, dropping stream", "ssrc", ns.ssrc)
					continue
				}
				// SCReAM has no unregister call, so reuse the registration left
				// behind by the previous bind.
				st.bound = true
				continue
			}
			queue := scream.NewQueue[*txPacket]()
			s.txMu.Lock()
			err := s.tx.RegisterNewStream(queue, ns.ssrc, ns.priority, ns.min, ns.start, ns.max)
			s.txMu.Unlock()
			if err != nil {
				s.logger.Error("failed to register stream", "ssrc", ns.ssrc, "error", err)
				continue
			}
			s.streams[ns.ssrc] = &screamStream{queue: queue, bound: true}
		case pkt := <-s.txQueue:
			stream, ok := s.streams[pkt.pkt.SSRC]
			if !ok || !stream.bound {
				s.logger.Error("got packet for unknown ssrc", "ssrc", pkt.pkt.SSRC)
				continue
			}
			stream.queue.Enqueue(pkt)
			s.txMu.Lock()
			s.tx.NewMediaFrame(pkt.ts, pkt.pkt.SSRC, pkt.Size(), pkt.pkt.Marker)
			s.txMu.Unlock()
		case pkt := <-s.rtcpRxQueue:
			s.receiveFeedback(pkt)
			if now := time.Now(); now.Sub(lastStats) >= time.Second && s.logger.Enabled(context.Background(), slog.LevelDebug) {
				lastStats = now
				s.txMu.Lock()
				stats := s.tx.GetStatistics(now)
				s.txMu.Unlock()
				s.logger.Debug("got scream statistics", "stats", stats)
			}
		case ssrc := <-s.removeStream:
			// Keep the mapping so a re-bind can reuse the SCReAM registration.
			if stream, ok := s.streams[ssrc]; ok {
				stream.bound = false
				stream.queue.Clear()
			}
		case <-timer.C:
		case <-s.closed:
			return
		}
		next := s.transmit()
		until := max(time.Until(next), time.Millisecond)
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

func (s *ScreamInterceptor) transmit() time.Time {
	for {
		now := time.Now()
		s.txMu.Lock()
		tx, ssrc := s.tx.IsOkToTransmit(now)
		s.txMu.Unlock()
		if tx < 0 {
			return now.Add(time.Second)
		}
		if tx > 0 {
			return now.Add(time.Duration(tx * float64(time.Second)))
		}
		stream, ok := s.streams[ssrc]
		if !ok {
			s.logger.Error("scream selected unknown ssrc", "ssrc", ssrc)
			return now.Add(time.Second)
		}
		pkt, ok := stream.queue.Dequeue()
		if !ok {
			return now.Add(time.Second)
		}
		if _, err := pkt.writer.Write(&pkt.pkt.Header, pkt.pkt.Payload, pkt.attr); err != nil {
			s.logger.Error("failed to write RTP packet", "err", err)
			return now.Add(time.Second)
		}
		now = time.Now()
		s.txMu.Lock()
		nextTx := s.tx.AddTransmitted(now, ssrc, pkt.Size(), pkt.SequenceNumber(), pkt.pkt.Marker)
		s.txMu.Unlock()
		if nextTx > 0 {
			return now.Add(time.Duration(nextTx * float64(time.Second)))
		}
	}
}

// BindLocalStream implements interceptor.Interceptor.
func (s *ScreamInterceptor) BindLocalStream(info *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	s.logger.Debug("binding interceptor", "info", fmt.Sprintf("%v", info))
	ns := &newStream{
		ssrc:     info.SSRC,
		priority: 1.0,
		min:      float64(s.min),
		max:      float64(s.max),
		start:    float64(s.init),
	}
	select {
	case s.newStreamQueue <- ns:
	case <-s.closed:
		return writer
	}
	return interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
		if attributes == nil {
			attributes = make(interceptor.Attributes)
		} else {
			attributes = maps.Clone(attributes)
		}
		payloadCopy := make([]byte, len(payload))
		copy(payloadCopy, payload)
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
		copy(rtcpCopy, b)
		select {
		case s.rtcpRxQueue <- &rxPacket{
			raw:  rtcpCopy,
			attr: maps.Clone(attr),
		}:
		case <-s.closed:
		}
		return n, attr, nil
	})
}

// Close implements interceptor.Interceptor.
func (s *ScreamInterceptor) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.wg.Wait()
		s.txMu.Lock()
		s.tx.Close()
		s.txClosed = true
		s.txMu.Unlock()
		if s.onClose != nil {
			s.onClose(s.id)
		}
	})
	return nil
}

// UnbindLocalStream implements interceptor.Interceptor.
func (s *ScreamInterceptor) UnbindLocalStream(info *interceptor.StreamInfo) {
	select {
	case s.removeStream <- info.SSRC:
	case <-s.closed:
	}
}
