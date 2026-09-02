package mrtp

import "context"

// Element is what a pipeline graph can own.
type Element interface {
	// Close releases the element's resources. It is safe to call more than
	// once.
	Close() error
}

// Sink is a push input: its upstream calls Write.
type Sink[T any] interface {
	Element

	// Negotiate configures the sink for format f, or rejects it. It is called
	// once every edge is wired, before the first Write, and again before the
	// first packet of a new format.
	Negotiate(f Format) error

	// Write consumes one packet and takes ownership of it. It must not block
	// indefinitely: an element that cannot keep up drops instead.
	Write(p Packet[T]) error

	// EndOfStream reports that no more packets will arrive.
	EndOfStream() error
}

// Source is a push output: it writes into the sink it is connected to.
type Source[T any] interface {
	Element

	// Format is what this source produces.
	Format() Format

	// Connect names the downstream sink.
	Connect(s Sink[T]) error
}

// Puller is a pull output: its downstream calls Pull.
type Puller[T any] interface {
	Element

	// Format is what this puller produces.
	Format() Format

	// Pull hands the next packet to the caller, io.EOF at the end of the
	// stream.
	Pull(ctx context.Context) (Packet[T], error)
}

// Consumer is a pull input: it reads from the puller it is attached to.
type Consumer[T any] interface {
	Element

	// Attach names the upstream puller.
	Attach(p Puller[T]) error
}

// Driver is an element that needs a goroutine of its own. Exactly one element
// per scheduling segment is a Driver.
type Driver interface {
	Element

	// Run moves packets until the stream ends, ctx is cancelled, or an error
	// occurs. It blocks, and returns nil at the end of the stream.
	Run(ctx context.Context) error
}
