package pipeline

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/mengelbart/mrtp"
)

// Policy is a queue's pacing and drop behaviour. The payload type is format
// specific, so the policy is what knows how to read it.
type Policy[T any] interface {
	// Boundary reports the last packet of an access unit, so that a full queue
	// drops whole frames. For RTP it is the marker bit.
	Boundary(*T) bool

	// Delay is how long to hold p after the previous packet left, before Pull
	// may return it, given how many packets are queued behind it.
	Delay(p *T, queued int) time.Duration
}

// A Failer is an upstream that wants to hear about an error its downstream hit
// in another segment. [Queue] is one, which is what carries a transport failure
// back to an encoder.
type Failer interface {
	Fail(err error)
}

// Queue decouples a pushing upstream from a pulling downstream. It is both an
// [mrtp.Sink] and an [mrtp.Puller], so it is wired with Connect on one side
// and Attach on the other, and it needs no context of its own because Pull
// carries one.
//
// It holds at most depth packets. A Write into a full queue drops the packets
// at the front up to and including the next access unit boundary rather than
// blocking, because back pressure into an encoder stalls the capture clock.
//
// One producer and one consumer.
type Queue[T any] struct {
	depth  int
	policy Policy[T]

	lock   sync.Mutex
	items  []mrtp.Packet[T]
	eos    bool
	failed error
	format mrtp.Format
	left   time.Time
	signal chan struct{}
}

func NewQueue[T any](depth int, p Policy[T]) *Queue[T] {
	if depth < 1 {
		depth = 1
	}
	return &Queue[T]{
		depth:  depth,
		policy: p,
		signal: make(chan struct{}, 1),
	}
}

// Negotiate implements mrtp.Sink. A queue carries what it is given.
func (q *Queue[T]) Negotiate(f mrtp.Format) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	q.format = f
	return nil
}

// Format implements mrtp.Puller.
func (q *Queue[T]) Format() mrtp.Format {
	q.lock.Lock()
	defer q.lock.Unlock()
	return q.format
}

// Write implements mrtp.Sink.
func (q *Queue[T]) Write(p mrtp.Packet[T]) error {
	q.lock.Lock()
	if q.failed != nil {
		err := q.failed
		q.lock.Unlock()
		p.Release()
		return err
	}
	if len(q.items) >= q.depth {
		q.dropLocked()
	}
	q.items = append(q.items, p)
	q.lock.Unlock()
	q.wake()
	return nil
}

// EndOfStream implements mrtp.Sink. Pull drains what is queued and then
// reports io.EOF.
func (q *Queue[T]) EndOfStream() error {
	q.lock.Lock()
	q.eos = true
	q.lock.Unlock()
	q.wake()
	return nil
}

// Fail implements Failer. The error surfaces at the producer, from the next
// Write.
func (q *Queue[T]) Fail(err error) {
	q.lock.Lock()
	if q.failed == nil {
		q.failed = err
	}
	q.lock.Unlock()
	q.wake()
}

// Pull implements mrtp.Puller. It blocks until a packet is due, the stream
// ends or ctx is cancelled.
func (q *Queue[T]) Pull(ctx context.Context) (mrtp.Packet[T], error) {
	for {
		q.lock.Lock()
		if len(q.items) > 0 {
			var delay time.Duration
			if q.policy != nil {
				delay = q.policy.Delay(q.items[0].Value(), len(q.items)-1)
			}
			now := time.Now()
			due := q.left.Add(delay)
			if !now.Before(due) {
				p := q.takeLocked(1)[0]
				q.left = now
				q.lock.Unlock()
				return p, nil
			}
			q.lock.Unlock()
			timer := time.NewTimer(due.Sub(now))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if q.eos {
			q.lock.Unlock()
			return nil, io.EOF
		}
		q.lock.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-q.signal:
		}
	}
}

// Close implements mrtp.Element. It releases what is still queued.
func (q *Queue[T]) Close() error {
	q.lock.Lock()
	defer q.lock.Unlock()
	for _, p := range q.takeLocked(len(q.items)) {
		p.Release()
	}
	return nil
}

// dropLocked makes room by releasing the packets up to and including the next
// access unit boundary, so what goes is the rest of a frame rather than an
// arbitrary packet. Without a policy, or without a boundary to find, it drops
// the packet at the front.
func (q *Queue[T]) dropLocked() {
	n := 1
	if q.policy != nil {
		for i, p := range q.items {
			if q.policy.Boundary(p.Value()) {
				n = i + 1
				break
			}
		}
	}
	for _, p := range q.takeLocked(n) {
		p.Release()
	}
}

// takeLocked removes the first n packets and hands their ownership to the
// caller.
func (q *Queue[T]) takeLocked(n int) []mrtp.Packet[T] {
	n = min(n, len(q.items))
	taken := make([]mrtp.Packet[T], n)
	copy(taken, q.items[:n])
	rest := copy(q.items, q.items[n:])
	clear(q.items[rest:])
	q.items = q.items[:rest]
	return taken
}

func (q *Queue[T]) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// PaceFrames spreads what is queued over 0.3 of a frame duration, so a frame's
// packets leave in a burst at the head of its interval and a backlog leaves
// faster the deeper it gets. The window is a fraction of the interval to leave
// a frame bigger than average, a keyframe above all, room to catch up in.
func PaceFrames[T any](boundary func(*T) bool, frameDuration time.Duration) Policy[T] {
	return &paceFrames[T]{
		boundary: boundary,
		burst:    time.Duration(0.3 * float64(frameDuration)),
	}
}

type paceFrames[T any] struct {
	boundary func(*T) bool
	burst    time.Duration
}

func (p *paceFrames[T]) Boundary(v *T) bool {
	return p.boundary(v)
}

func (p *paceFrames[T]) Delay(_ *T, queued int) time.Duration {
	if queued <= 0 {
		return 0
	}
	if d := p.burst / time.Duration(queued); d > 0 {
		return d
	}
	return time.Microsecond
}

var (
	_ mrtp.Sink[int]   = (*Queue[int])(nil)
	_ mrtp.Puller[int] = (*Queue[int])(nil)
	_ Failer           = (*Queue[int])(nil)
)
