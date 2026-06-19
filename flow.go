package mrtp

type Flow struct {
	Input         Source
	Output        Sink
	Containerizer Containerizer
}

type Containerizer interface {
	Containerize([]byte) ([][]byte, error)
}
