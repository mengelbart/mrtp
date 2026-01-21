package mrtp

import "io"

type Sink interface {
	io.WriteCloser
}
