package mediafile

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp"
)

const (
	ivfFileHeaderSize  = 32
	ivfFrameHeaderSize = 12
	// ivfFrameCountOffset is where the file header keeps the number of frames.
	ivfFrameCountOffset = 24
	// ivfTimebase is the unit of the frame timestamps the sink writes.
	ivfTimebase = time.Millisecond
)

// IVFSink writes the VP8 or VP9 frames it is given to an IVF file. Frame
// timestamps are in milliseconds from the first frame.
type IVFSink struct {
	writer io.WriteCloser
	format mrtp.EncodedVideo

	headerWritten bool
	firstPTS      time.Duration
	frames        atomic.Uint64
}

// NewIVFSink returns a sink that writes to writer. If writer is an
// io.WriteSeeker, Close fills in the frame count of the header.
func NewIVFSink(writer io.WriteCloser) *IVFSink {
	return &IVFSink{writer: writer}
}

// Negotiate implements mrtp.Sink.
func (s *IVFSink) Negotiate(f mrtp.Format) error {
	video, ok := f.(mrtp.EncodedVideo)
	if !ok {
		return fmt.Errorf("IVF sink takes encoded video, not %v", f)
	}
	if _, err := fourCCFromCodec(video.Codec); err != nil {
		return err
	}
	if s.headerWritten && video.Codec != s.format.Codec {
		return fmt.Errorf("IVF sink cannot change codec from %v to %v mid-file", s.format.Codec, video.Codec)
	}
	s.format = video
	return nil
}

// Write implements mrtp.Sink.
func (s *IVFSink) Write(p mrtp.Packet[mrtp.EncodedFrame]) error {
	defer p.Release()
	frame := p.Value()
	if !s.headerWritten {
		if err := s.writeFileHeader(); err != nil {
			return err
		}
		s.firstPTS = frame.PTS
	}
	header := make([]byte, ivfFrameHeaderSize)
	binary.LittleEndian.PutUint32(header[0:], uint32(len(frame.Data)))
	binary.LittleEndian.PutUint64(header[4:], uint64((frame.PTS-s.firstPTS)/ivfTimebase))
	if _, err := s.writer.Write(header); err != nil {
		return err
	}
	if _, err := s.writer.Write(frame.Data); err != nil {
		return err
	}
	s.frames.Add(1)
	return nil
}

// Frames returns the number of frames written.
func (s *IVFSink) Frames() uint64 {
	return s.frames.Load()
}

func (s *IVFSink) writeFileHeader() error {
	fourCC, err := fourCCFromCodec(s.format.Codec)
	if err != nil {
		return err
	}
	header := make([]byte, ivfFileHeaderSize)
	copy(header[0:], "DKIF")
	binary.LittleEndian.PutUint16(header[4:], 0) // version
	binary.LittleEndian.PutUint16(header[6:], ivfFileHeaderSize)
	copy(header[8:], fourCC)
	binary.LittleEndian.PutUint16(header[12:], uint16(s.format.Width))
	binary.LittleEndian.PutUint16(header[14:], uint16(s.format.Height))
	binary.LittleEndian.PutUint32(header[16:], uint32(time.Second/ivfTimebase)) // timebase denominator
	binary.LittleEndian.PutUint32(header[20:], 1)                               // timebase numerator
	if _, err = s.writer.Write(header); err != nil {
		return err
	}
	s.headerWritten = true
	return nil
}

// EndOfStream implements mrtp.Sink.
func (s *IVFSink) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element. It fills in the frame count if it can seek.
func (s *IVFSink) Close() error {
	var err error
	if seeker, ok := s.writer.(io.WriteSeeker); ok && s.headerWritten {
		count := make([]byte, 4)
		binary.LittleEndian.PutUint32(count, uint32(s.frames.Load()))
		if _, err = seeker.Seek(ivfFrameCountOffset, io.SeekStart); err == nil {
			_, err = seeker.Write(count)
		}
	}
	return errors.Join(err, s.writer.Close())
}

// fourCCFromCodec is the inverse of codecFromFourCC.
func fourCCFromCodec(c mrtp.Codec) (string, error) {
	switch c {
	case mrtp.VP8:
		return "VP80", nil
	case mrtp.VP9:
		return "VP90", nil
	}
	return "", fmt.Errorf("IVF sink cannot write %v", c)
}

var _ mrtp.Sink[mrtp.EncodedFrame] = (*IVFSink)(nil)
