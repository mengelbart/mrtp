package gopipe

import (
	"log"
	"testing"

	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/stretchr/testify/assert"
)

func TestPipeline(t *testing.T) {
	sink := WriterFunc(func(b []byte, a Attributes) error {
		log.Println("sinking buffer")
		return nil
	})
	e := NewEncoder(codec.VP8)
	i := Info{
		Width:       720,
		Height:      480,
		TimebaseNum: 30,
		TimebaseDen: 1,
	}

	source, err := Chain(i, sink, e)
	assert.NoError(t, err)

	source.Write(nil, Attributes{})
}
