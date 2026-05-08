package ivf

import (
	"io"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
)

type Sink struct {
	writer *ivfwriter.IVFWriter
}

func NewSink(wc io.WriteCloser) (*Sink, error) {
	ivfWriter, err := ivfwriter.NewWith(wc)
	if err != nil {
		return nil, err
	}
	return &Sink{
		writer: ivfWriter,
	}, nil
}

func (s *Sink) Write(buf []byte) (int, error) {
	pkt := &rtp.Packet{}
	if err := pkt.Unmarshal(buf); err != nil {
		return 0, err
	}
	if err := s.writer.WriteRTP(pkt); err != nil {
		return 0, err
	}
	return len(buf), nil
}

func (s *Sink) Close() error {
	return s.writer.Close()
}
