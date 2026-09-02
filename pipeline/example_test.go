package pipeline_test

import (
	"context"
	"fmt"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// source is a push source of a few chunks. A source that produces on its own is
// also a Driver, so the graph gives it a goroutine.
type source struct {
	pool  *pipeline.Pool[mrtp.DataChunk]
	words []string
	down  mrtp.Sink[mrtp.DataChunk]
}

func (s *source) Format() mrtp.Format { return mrtp.Data{} }

func (s *source) Connect(down mrtp.Sink[mrtp.DataChunk]) error {
	s.down = down
	return nil
}

func (s *source) Run(context.Context) error {
	for _, word := range s.words {
		chunk := s.pool.Get()
		chunk.Value().Data = append(chunk.Value().Data, word...)
		// Write hands the chunk on: the source must not touch it again.
		if err := s.down.Write(chunk); err != nil {
			return err
		}
	}
	return s.down.EndOfStream()
}

func (s *source) Close() error { return nil }

// sink is a push sink. Write takes ownership of what it is handed, so it
// releases the chunk when it is done with it.
type sink struct{}

func (*sink) Negotiate(f mrtp.Format) error {
	fmt.Println("negotiated", f)
	return nil
}

func (*sink) Write(chunk mrtp.Packet[mrtp.DataChunk]) error {
	defer chunk.Release()
	fmt.Println("chunk", string(chunk.Value().Data))
	return nil
}

func (*sink) EndOfStream() error {
	fmt.Println("end of stream")
	return nil
}

func (*sink) Close() error { return nil }

// Example builds one push segment, a source writing into a sink, and runs it.
func Example() {
	pool := pipeline.NewPool(
		func() *mrtp.DataChunk { return &mrtp.DataChunk{Data: make([]byte, 0, 16)} },
		func(c *mrtp.DataChunk) { c.Data = c.Data[:0] },
	)

	g := pipeline.NewGraph()
	src := &source{pool: pool, words: []string{"one", "two"}}
	if err := g.Connect(src, &sink{}); err != nil {
		fmt.Println(err)
		return
	}
	// The source running out of chunks ends the graph.
	g.Terminal(src)

	if err := g.Run(context.Background()); err != nil {
		fmt.Println(err)
		return
	}
	if err := g.Close(); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("chunks still owned by someone:", pool.Outstanding())

	// Output:
	// negotiated data
	// chunk one
	// chunk two
	// end of stream
	// chunks still owned by someone: 0
}
