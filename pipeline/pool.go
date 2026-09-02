package pipeline

import (
	"sync"
	"sync/atomic"

	"github.com/mengelbart/mrtp"
)

// Pool hands out pooled values of type T.
type Pool[T any] struct {
	alloc func() *T
	reset func(*T)

	free        sync.Pool
	outstanding atomic.Int64
}

// NewPool returns a pool of values built by alloc. reset puts a value back into
// the state alloc gave it and may be nil.
func NewPool[T any](alloc func() *T, reset func(*T)) *Pool[T] {
	return &Pool[T]{
		alloc: alloc,
		reset: reset,
	}
}

// Get takes a value from the pool as a packet the caller owns.
func (p *Pool[T]) Get() *Ref[T] {
	c, _ := p.free.Get().(*control[T])
	if c == nil {
		c = &control[T]{pool: p, value: p.alloc()}
	}
	c.refs.Store(1)
	p.outstanding.Add(1)
	return &Ref[T]{control: c}
}

// Outstanding is the number of values Get has handed out whose last reference
// has not been released yet.
func (p *Pool[T]) Outstanding() int {
	return int(p.outstanding.Load())
}

// control is the state a value's references share.
type control[T any] struct {
	pool  *Pool[T]
	value *T
	refs  atomic.Int64
}

// Ref is a reference counted, pool backed [mrtp.Packet]. Clone hands out
// another reference to the same value, and the value goes back to its pool when
// the last reference is released.
//
// Every reference is released exactly once. Releasing one twice, or touching
// the value through a released reference, panics rather than corrupting the
// pool.
type Ref[T any] struct {
	control  *control[T]
	released atomic.Bool
}

// Value implements mrtp.Packet.
func (r *Ref[T]) Value() *T {
	if r.released.Load() {
		panic("pipeline: Value on a released packet")
	}
	return r.control.value
}

// Clone implements mrtp.Packet.
func (r *Ref[T]) Clone() mrtp.Packet[T] {
	if r.released.Load() {
		panic("pipeline: Clone of a released packet")
	}
	r.control.refs.Add(1)
	return &Ref[T]{control: r.control}
}

// Release implements mrtp.Packet.
func (r *Ref[T]) Release() {
	if !r.released.CompareAndSwap(false, true) {
		panic("pipeline: packet released twice")
	}
	c := r.control
	switch n := c.refs.Add(-1); {
	case n > 0:
		return
	case n < 0:
		panic("pipeline: packet reference count below zero")
	}
	c.pool.outstanding.Add(-1)
	if poolDebug {
		// Poison rather than recycle, so a value that is still reachable
		// through a stale pointer cannot silently hold another packet's data.
		var zero T
		*c.value = zero
		return
	}
	if c.pool.reset != nil {
		c.pool.reset(c.value)
	}
	c.pool.free.Put(c)
}
