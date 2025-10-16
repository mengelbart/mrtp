package webrtc

import (
	"errors"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"golang.org/x/time/rate"
)

type PacingInterceptorFactory struct {
	lock         sync.Mutex
	interceptors map[string]*PacingInterceptor
}

func newPacingInterceptorFactory() *PacingInterceptorFactory {
	return &PacingInterceptorFactory{
		lock:         sync.Mutex{},
		interceptors: map[string]*PacingInterceptor{},
	}
}

func (f *PacingInterceptorFactory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	i := &PacingInterceptor{
		NoOp:   interceptor.NoOp{},
		limit:  rate.NewLimiter(rate.Limit(750_000), 1200*8*2),
		closed: make(chan struct{}),
		wg:     sync.WaitGroup{},
		queue:  make(chan packet, 1_000_000),
	}
	f.interceptors[id] = i

	i.wg.Go(i.loop)

	return i, nil
}

func (f *PacingInterceptorFactory) SetRate(id string, r int) {
	f.lock.Lock()
	defer f.lock.Unlock()

	i, ok := f.interceptors[id]
	if !ok {
		return
	}
	i.limit.SetLimit(rate.Limit(r))
}

type packet struct {
	writer     interceptor.RTPWriter
	header     *rtp.Header
	payload    []byte
	attributes interceptor.Attributes
}

func (p *packet) len() int {
	return p.header.MarshalSize() + len(p.payload)
}

type PacingInterceptor struct {
	interceptor.NoOp
	limit  *rate.Limiter
	closed chan struct{}
	wg     sync.WaitGroup
	queue  chan packet
}

// BindLocalStream implements interceptor.Interceptor.
func (p *PacingInterceptor) BindLocalStream(info *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	return interceptor.RTPWriterFunc(func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
		hdr := header.Clone()
		pay := make([]byte, len(payload))
		copy(pay, payload)
		attr := maps.Clone(attributes)
		select {
		case p.queue <- packet{
			writer:     writer,
			header:     &hdr,
			payload:    pay,
			attributes: attr,
		}:
		case <-p.closed:
			return 0, errors.New("pacer closed")
		default:
			return 0, errors.New("pacer queue overflow")
		}
		return header.MarshalSize() + len(payload), nil
	})
}

// Close implements interceptor.Interceptor.
func (p *PacingInterceptor) Close() error {
	defer p.wg.Done()
	return nil
}

func (p *PacingInterceptor) loop() {
	ticker := time.NewTicker(5 * time.Millisecond)
	queue := make([]packet, 0)
	for {
		select {
		case now := <-ticker.C:
			sent := 0
			for len(queue) > 0 && p.limit.TokensAt(now) > 8*float64(queue[0].len()) {
				p.limit.AllowN(now, queue[0].len())
				var next packet
				next, queue = queue[0], queue[1:]
				n, err := next.writer.Write(next.header, next.payload, next.attributes)
				sent += n
				if err != nil {
					slog.Warn("error on writing RTP packet", "error", err)
				}
			}
			slog.Info("pacing interval done", "queue_len", len(queue), "bytes_sent", sent)
		case pkt := <-p.queue:
			queue = append(queue, pkt)
		case <-p.closed:
			return
		}
	}
}
