package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/mengelbart/mrtp"
)

// An Edge is one connection between two elements, upstream first.
type Edge struct {
	Up, Down mrtp.Element
}

// OwnedPorts is implemented by an element that wires ports of its own, beyond
// the single port each port interface gives it: [Tee]'s outputs, [Funnel]'s
// inputs and [Scheduler]'s inputs. The element negotiates those ports itself,
// and the graph uses the edges only to order the rest of the wiring around them
// and to close what they reach.
type OwnedPorts interface {
	// OwnedPorts reports the element's own edges, upstream first.
	OwnedPorts() []Edge
}

// Graph owns a set of elements and the edges between them. Wiring records
// edges, Run binds and negotiates them and drives the graph, and Close releases
// the elements.
type Graph struct {
	elements []mrtp.Element
	seen     map[mrtp.Element]bool
	edges    []edge
	terminal map[mrtp.Element]bool
}

// edge is a recorded connection together with the way to wire it up.
type edge struct {
	Edge

	// bind connects the two ends and negotiates the format across them. It is
	// what a push and a pull edge differ in.
	bind func() error
}

func NewGraph() *Graph {
	return &Graph{
		seen:     map[mrtp.Element]bool{},
		terminal: map[mrtp.Element]bool{},
	}
}

// Connect records a push edge. It does not wire or negotiate it: Run does that,
// once every edge is in place.
func (g *Graph) Connect[T any](up mrtp.Source[T], down mrtp.Sink[T]) error {
	if up == nil || down == nil {
		return errors.New("pipeline: Connect needs a source and a sink")
	}
	g.Add(up)
	g.Add(down)
	g.edges = append(g.edges, edge{
		Up:   up,
		Down: down,
		bind: func() error {
			if err := up.Connect(down); err != nil {
				return err
			}
			return down.Negotiate(up.Format())
		},
	})
	return nil
}

// Attach records a pull edge. It does not wire it: Run does that, once the
// puller's format is settled.
func (g *Graph) Attach[T any](up mrtp.Puller[T], down mrtp.Consumer[T]) error {
	if up == nil || down == nil {
		return errors.New("pipeline: Attach needs a puller and a consumer")
	}
	g.Add(up)
	g.Add(down)
	g.edges = append(g.edges, edge{
		Up:   up,
		Down: down,
		bind: func() error { return down.Attach(up) },
	})
	return nil
}

// Add registers an element that neither Connect nor Attach reached, such as a
// driver in the middle of a graph.
func (g *Graph) Add(e mrtp.Element) {
	if e == nil || g.seen[e] {
		return
	}
	g.seen[e] = true
	g.elements = append(g.elements, e)
}

// Terminal marks a driver whose completion ends the graph. Without one, Run
// returns only when every driver has returned.
func (g *Graph) Terminal(d mrtp.Driver) {
	g.Add(d)
	g.terminal[d] = true
}

// Run binds and negotiates every edge, then gives each driver a goroutine and
// blocks. The first error cancels the rest, and so does the completion of a
// terminal driver.
func (g *Graph) Run(ctx context.Context) error {
	if err := g.bind(); err != nil {
		return err
	}
	var drivers []mrtp.Driver
	for _, e := range g.elements {
		if d, ok := e.(mrtp.Driver); ok {
			drivers = append(drivers, d)
		}
	}
	if len(drivers) == 0 {
		return errors.New("pipeline: graph has no driver")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu           sync.Mutex
		firstErr     error
		terminalDone bool
		wg           sync.WaitGroup
	)
	for _, d := range drivers {
		wg.Go(func() {
			err := d.Run(runCtx)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if g.terminal[d] {
				terminalDone = true
			}
			mu.Unlock()
			if err != nil || g.terminal[d] {
				cancel()
			}
		})
	}
	wg.Wait()

	// A driver that was cancelled because the terminal driver finished has
	// done its job, so its context error is not the graph's.
	if terminalDone && ctx.Err() == nil && errors.Is(firstErr, context.Canceled) {
		return nil
	}
	return firstErr
}

// Close closes every registered element. It is called after Run returns.
func (g *Graph) Close() error {
	var errs []error
	for _, e := range g.elements {
		if err := e.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// bind wires and negotiates every recorded edge, in an order that reaches an
// element only once everything upstream of it is configured.
func (g *Graph) bind() error {
	order, err := g.order()
	if err != nil {
		return err
	}
	position := make(map[mrtp.Element]int, len(order))
	for i, e := range order {
		position[e] = i
	}
	edges := slices.Clone(g.edges)
	slices.SortStableFunc(edges, func(a, b edge) int {
		return position[a.Up] - position[b.Up]
	})
	for _, e := range edges {
		if err := e.bind(); err != nil {
			return fmt.Errorf("pipeline: wiring %T to %T: %w", e.Up, e.Down, err)
		}
	}
	return nil
}

// order sorts the elements so that an element comes after everything upstream
// of it. It registers the elements that only an owned port reaches.
func (g *Graph) order() ([]mrtp.Element, error) {
	constraints := make([]Edge, 0, len(g.edges))
	for _, e := range g.edges {
		constraints = append(constraints, e.Edge)
	}
	// The loop reads a slice it appends to, so an element reached through an
	// owned port is asked for its own ports in turn.
	for i := 0; i < len(g.elements); i++ {
		owner, ok := g.elements[i].(OwnedPorts)
		if !ok {
			continue
		}
		for _, e := range owner.OwnedPorts() {
			g.Add(e.Up)
			g.Add(e.Down)
			constraints = append(constraints, e)
		}
	}
	// Below is Kahn's algorithm for topological sort.
	indegree := make(map[mrtp.Element]int, len(g.elements))
	downstream := make(map[mrtp.Element][]mrtp.Element, len(g.elements))
	for _, c := range constraints {
		downstream[c.Up] = append(downstream[c.Up], c.Down)
		indegree[c.Down]++
	}
	var (
		ready []mrtp.Element
		order = make([]mrtp.Element, 0, len(g.elements))
	)
	for _, e := range g.elements {
		if indegree[e] == 0 {
			ready = append(ready, e)
		}
	}
	for len(ready) > 0 {
		e := ready[0]
		ready = ready[1:]
		order = append(order, e)
		for _, down := range downstream[e] {
			indegree[down]--
			if indegree[down] == 0 {
				ready = append(ready, down)
			}
		}
	}
	if len(order) != len(g.elements) {
		return nil, errors.New("pipeline: graph has a cycle")
	}
	return order, nil
}
