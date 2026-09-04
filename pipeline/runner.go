package pipeline

import (
	"context"
	"errors"
	"io"
	"sync"
)

// Runner runs a set of graphs. Graphs may join while it is running.
//
// A graph ends the run by failing, or, if it has a terminal driver, by
// completing. The runner owns the graphs it is given and closes them.
type Runner struct {
	lock sync.Mutex
	// ctx is nil until Run is called. Graphs added before that wait in
	// pending, graphs added afterwards start immediately.
	ctx     context.Context
	pending []*Graph
	graphs  []*Graph

	// done carries the first event that ends the run.
	done chan error
}

func NewRunner() *Runner {
	return &Runner{done: make(chan error, 1)}
}

// Add takes ownership of g and runs it, or queues it until Run starts.
func (r *Runner) Add(g *Graph) {
	if g == nil {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	r.graphs = append(r.graphs, g)
	if r.ctx == nil {
		r.pending = append(r.pending, g)
		return
	}
	r.launch(r.ctx, g)
}

// Run drives every graph until one of them ends the run or ctx is cancelled.
// It blocks.
func (r *Runner) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	r.lock.Lock()
	if r.ctx != nil {
		r.lock.Unlock()
		return errors.New("pipeline: runner is already running")
	}
	r.ctx = runCtx
	pending := r.pending
	r.pending = nil
	r.lock.Unlock()

	for _, g := range pending {
		r.launch(runCtx, g)
	}

	select {
	case err := <-r.done:
		return err
	case <-runCtx.Done():
		return nil
	}
}

// Close closes every graph the runner was given. It is safe to call after Run
// has returned.
func (r *Runner) Close() error {
	r.lock.Lock()
	graphs := r.graphs
	r.graphs = nil
	r.lock.Unlock()

	var errs []error
	for _, g := range graphs {
		if err := g.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// launch drives one graph, reporting the first event that ends the run.
func (r *Runner) launch(ctx context.Context, g *Graph) {
	go func() {
		err := g.Run(ctx)
		// A cancelled graph is the run shutting down, and io.EOF is a
		// downstream that stopped taking packets. Neither is a failure.
		if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			err = nil
		}
		if err == nil && !g.HasTerminal() {
			return
		}
		select {
		case r.done <- err:
		default:
			// Run is already returning with an earlier event.
		}
	}()
}
