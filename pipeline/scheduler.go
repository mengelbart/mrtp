package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/mengelbart/mrtp"
)

// A Selector chooses which of a scheduler's inputs supplies the next packet.
type Selector[T any] interface {
	// Select is called with one entry per input, nil where that input has no
	// packet ready, and returns the index of the input to take from. At least
	// one entry is not nil.
	Select(ready []*T) int
}

// Scheduler merges N pull inputs into one pull output, choosing per packet by
// a policy. It is where priority between the flows sharing one connection
// belongs.
//
// It keeps one packet staged per input, because a puller cannot be asked
// whether it has one without taking it, so it holds a goroutine per input for
// as long as it runs. The first Pull starts them and Close stops them.
type Scheduler[T any] struct {
	selector Selector[T]
	inputs   []*schedulerInput[T]

	lock    sync.Mutex
	started bool
	cancel  context.CancelFunc
	err     error
	ready   chan struct{}
}

func NewScheduler[T any](s Selector[T]) *Scheduler[T] {
	return &Scheduler[T]{
		selector: s,
		ready:    make(chan struct{}, 1),
	}
}

// AddInput adds a puller to choose from. Every input must carry the same
// format, because they share one output.
func (s *Scheduler[T]) AddInput(p mrtp.Puller[T]) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.inputs = append(s.inputs, &schedulerInput[T]{
		puller: p,
		want:   make(chan struct{}, 1),
	})
}

// OwnedPorts implements [OwnedPorts].
func (s *Scheduler[T]) OwnedPorts() []Edge {
	s.lock.Lock()
	defer s.lock.Unlock()
	edges := make([]Edge, 0, len(s.inputs))
	for _, in := range s.inputs {
		edges = append(edges, Edge{Up: in.puller, Down: s})
	}
	return edges
}

// Format implements mrtp.Puller. It is the format the inputs agree on: a
// disagreement is recorded here and reported by Pull, because a format has no
// error to return.
func (s *Scheduler[T]) Format() mrtp.Format {
	s.lock.Lock()
	defer s.lock.Unlock()
	if len(s.inputs) == 0 {
		return nil
	}
	format := s.inputs[0].puller.Format()
	for _, in := range s.inputs[1:] {
		if other := in.puller.Format(); format != other {
			if s.err == nil {
				s.err = fmt.Errorf("pipeline: scheduler inputs carry %v and %v", format, other)
			}
		}
	}
	return format
}

// Pull implements mrtp.Puller. It reports io.EOF once every input has ended.
func (s *Scheduler[T]) Pull(ctx context.Context) (mrtp.Packet[T], error) {
	s.lock.Lock()
	if s.err != nil {
		err := s.err
		s.lock.Unlock()
		return nil, err
	}
	if len(s.inputs) == 0 {
		s.lock.Unlock()
		return nil, io.EOF
	}
	s.startLocked(ctx)
	s.lock.Unlock()

	staged := make([]*T, len(s.inputs))
	for {
		var (
			available int
			ended     int
			failed    error
		)
		for i, in := range s.inputs {
			packet, err := in.staged()
			staged[i] = nil
			switch {
			case packet != nil:
				staged[i] = packet.Value()
				available++
			case errors.Is(err, io.EOF):
				ended++
			case err != nil:
				if failed == nil {
					failed = err
				}
			}
		}
		if failed != nil {
			return nil, failed
		}
		if available > 0 {
			i := s.selector.Select(staged)
			if i < 0 || i >= len(s.inputs) || staged[i] == nil {
				return nil, fmt.Errorf("pipeline: selector chose input %d, which has no packet", i)
			}
			return s.inputs[i].take(), nil
		}
		if ended == len(s.inputs) {
			return nil, io.EOF
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.ready:
		}
	}
}

// Close implements mrtp.Element. It stops the input goroutines and releases
// what they staged.
func (s *Scheduler[T]) Close() error {
	s.lock.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.lock.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, in := range s.inputs {
		in.release()
	}
	return nil
}

// startLocked gives every input a goroutine that keeps one packet staged. It
// runs on the context of the first Pull, which is the driver's.
func (s *Scheduler[T]) startLocked(ctx context.Context) {
	if s.started {
		return
	}
	s.started = true
	ctx, s.cancel = context.WithCancel(ctx)
	for _, in := range s.inputs {
		in.want <- struct{}{}
		go in.run(ctx, s.wake)
	}
}

func (s *Scheduler[T]) wake() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// schedulerInput is one input's staging slot.
type schedulerInput[T any] struct {
	puller mrtp.Puller[T]
	want   chan struct{}

	lock   sync.Mutex
	packet mrtp.Packet[T]
	err    error
}

// run keeps the slot filled: one packet is pulled ahead, and the next is
// pulled once that one has been taken.
func (i *schedulerInput[T]) run(ctx context.Context, wake func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-i.want:
		}
		packet, err := i.puller.Pull(ctx)
		i.lock.Lock()
		i.packet, i.err = packet, err
		i.lock.Unlock()
		wake()
		if err != nil {
			return
		}
	}
}

func (i *schedulerInput[T]) staged() (mrtp.Packet[T], error) {
	i.lock.Lock()
	defer i.lock.Unlock()
	return i.packet, i.err
}

// take hands the staged packet to the caller and asks for the next.
func (i *schedulerInput[T]) take() mrtp.Packet[T] {
	i.lock.Lock()
	packet := i.packet
	i.packet = nil
	i.lock.Unlock()
	select {
	case i.want <- struct{}{}:
	default:
	}
	return packet
}

func (i *schedulerInput[T]) release() {
	i.lock.Lock()
	defer i.lock.Unlock()
	if i.packet != nil {
		i.packet.Release()
		i.packet = nil
	}
}

// Priority takes from the first input that has a packet, so the input added
// first is served first.
func Priority[T any]() Selector[T] {
	return selectorFunc[T](func(ready []*T) int {
		for i, p := range ready {
			if p != nil {
				return i
			}
		}
		return -1
	})
}

// Weights shares the output between the inputs in proportion to weights,
// counted in packets. An input the weights do not reach counts as 1.
func Weights[T any](weights ...float64) Selector[T] {
	return &weighted[T]{weights: weights}
}

// weighted is weighted fair queueing over packets: every input has a virtual
// time that advances by 1/weight per packet taken, and the ready input with
// the smallest virtual time goes next.
type weighted[T any] struct {
	weights []float64
	virtual []float64
}

func (w *weighted[T]) weight(i int) float64 {
	if i < len(w.weights) && w.weights[i] > 0 {
		return w.weights[i]
	}
	return 1
}

func (w *weighted[T]) Select(ready []*T) int {
	for len(w.virtual) < len(ready) {
		w.virtual = append(w.virtual, w.earliest())
	}
	chosen := -1
	for i, p := range ready {
		if p == nil {
			continue
		}
		if chosen < 0 || w.virtual[i] < w.virtual[chosen] {
			chosen = i
		}
	}
	if chosen >= 0 {
		w.virtual[chosen] += 1 / w.weight(chosen)
	}
	return chosen
}

// earliest is the virtual time an input joining now starts from, so it neither
// starves nor takes a burst to catch up.
func (w *weighted[T]) earliest() float64 {
	if len(w.virtual) == 0 {
		return 0
	}
	earliest := w.virtual[0]
	for _, v := range w.virtual[1:] {
		earliest = min(earliest, v)
	}
	return earliest
}

type selectorFunc[T any] func(ready []*T) int

func (f selectorFunc[T]) Select(ready []*T) int { return f(ready) }

var (
	_ mrtp.Puller[int] = (*Scheduler[int])(nil)
	_ OwnedPorts       = (*Scheduler[int])(nil)
)
