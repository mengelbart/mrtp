package codec

type AttributeKey int

const (
	ChromaSubsampling AttributeKey = iota
	IsKeyFrame
	SampleDuration
	Width
	Height
	PTS
	FrameDuration
)

type Info struct {
	Width       uint
	Height      uint
	TimebaseNum int
	TimebaseDen int
}

type Attributes map[any]any

type Writer interface {
	Write([]byte, Attributes) error
}

type WriterFunc func([]byte, Attributes) error

func (f WriterFunc) Write(b []byte, a Attributes) error {
	return f(b, a)
}

type MultiWriter interface {
	Writer
	WriteAll([][]byte, Attributes) error
}

type Processor interface {
	Link(Writer, Info) (Writer, error)
}

func Chain(i Info, f Writer, processors ...Processor) (Writer, error) {
	var err error
	for _, p := range processors {
		f, err = p.Link(f, i)
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}
