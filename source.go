package mrtp

import "io"

type Source interface {
	io.ReadCloser
}
