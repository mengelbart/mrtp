package mediafile

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// IVFSource releases the frames of an IVF file at the times their
// timestamps say, starting with the first frame on the first Pull.
type IVFSource struct {
	reader io.ReadCloser
	// timebaseNum/timebaseDen seconds is the unit of frame timestamps.
	timebaseNum, timebaseDen uint32
	// frames is the frame count of the file header.
	frames uint32

	// ahead are the next frames of the file, at most two, so that the
	// duration of a frame is known when it is released.
	ahead []ivfFrame
	// err is why reading ahead stopped, nil at the end of the file.
	err error
	// frameDuration is the interval between the first two frames.
	frameDuration time.Duration
	firstPTS      time.Duration
	start         time.Time

	format mrtp.EncodedVideo
	pool   *pipeline.Pool[mrtp.EncodedFrame]
}

type ivfFrame struct {
	data []byte
	pts  time.Duration
}

// codecFromFourCC maps an IVF FourCC to the codec of its frames.
func codecFromFourCC(fourCC string) (mrtp.Codec, error) {
	switch fourCC {
	case "VP80":
		return mrtp.VP8, nil
	case "VP90":
		return mrtp.VP9, nil
	}
	return 0, fmt.Errorf("unsupported IVF FourCC: %q", fourCC)
}

// NewIVFSource reads the header and the first frames of an IVF file.
func NewIVFSource(reader io.ReadCloser) (*IVFSource, error) {
	header := make([]byte, ivfFileHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, fmt.Errorf("reading IVF file header: %w", err)
	}
	if string(header[0:4]) != "DKIF" {
		return nil, errors.New("not an IVF file")
	}
	if size := binary.LittleEndian.Uint16(header[6:]); size != ivfFileHeaderSize {
		return nil, fmt.Errorf("unsupported IVF header size %v", size)
	}
	codec, err := codecFromFourCC(string(header[8:12]))
	if err != nil {
		return nil, err
	}
	s := &IVFSource{
		reader:      reader,
		timebaseDen: binary.LittleEndian.Uint32(header[16:]),
		timebaseNum: binary.LittleEndian.Uint32(header[20:]),
		frames:      binary.LittleEndian.Uint32(header[ivfFrameCountOffset:]),
		format: mrtp.EncodedVideo{
			Codec:  codec,
			Width:  uint(binary.LittleEndian.Uint16(header[12:])),
			Height: uint(binary.LittleEndian.Uint16(header[14:])),
		},
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
	}
	if s.timebaseNum == 0 || s.timebaseDen == 0 {
		return nil, fmt.Errorf("invalid IVF timebase %v/%v", s.timebaseNum, s.timebaseDen)
	}
	s.readAhead()
	s.readAhead()
	switch {
	case len(s.ahead) == 0 && !errors.Is(s.err, io.EOF):
		return nil, s.err
	case len(s.ahead) == 0:
		return nil, errors.New("IVF file has no frames")
	case len(s.ahead) == 2 && s.ahead[1].pts > s.ahead[0].pts:
		s.frameDuration = s.ahead[1].pts - s.ahead[0].pts
	default:
		// A file of one frame plays it for one tick of the timebase.
		s.frameDuration = s.toDuration(1)
	}
	s.firstPTS = s.ahead[0].pts
	return s, nil
}

// readAhead reads the next frame of the file, if there is one.
func (s *IVFSource) readAhead() {
	if s.err != nil {
		return
	}
	header := make([]byte, ivfFrameHeaderSize)
	if _, err := io.ReadFull(s.reader, header); err != nil {
		if !errors.Is(err, io.EOF) {
			err = fmt.Errorf("reading IVF frame header: %w", err)
		}
		s.err = err
		return
	}
	data := make([]byte, binary.LittleEndian.Uint32(header[0:]))
	if _, err := io.ReadFull(s.reader, data); err != nil {
		s.err = fmt.Errorf("reading IVF frame: %w", err)
		return
	}
	s.ahead = append(s.ahead, ivfFrame{data: data, pts: s.toDuration(binary.LittleEndian.Uint64(header[4:]))})
}

// toDuration converts a count of timebase ticks to a duration.
func (s *IVFSource) toDuration(ticks uint64) time.Duration {
	return time.Duration(ticks) * time.Second * time.Duration(s.timebaseNum) / time.Duration(s.timebaseDen)
}

// FrameDuration is the interval between the first two frames of the file.
func (s *IVFSource) FrameDuration() time.Duration {
	return s.frameDuration
}

// Format implements mrtp.Puller.
func (s *IVFSource) Format() mrtp.Format {
	return s.format
}

// Pull implements mrtp.Puller. After the last frame it reports io.EOF, or why
// the file could not be read further.
func (s *IVFSource) Pull(ctx context.Context) (mrtp.Packet[mrtp.EncodedFrame], error) {
	if len(s.ahead) == 0 {
		return nil, s.err
	}
	frame := s.ahead[0]
	pts := frame.pts - s.firstPTS
	if s.start.IsZero() {
		s.start = time.Now()
	} else {
		timer := time.NewTimer(time.Until(s.start.Add(pts)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	duration := s.frameDuration
	if len(s.ahead) > 1 {
		duration = s.ahead[1].pts - frame.pts
	}
	s.ahead = s.ahead[1:]
	s.readAhead()

	packet := s.pool.Get()
	value := packet.Value()
	value.Data = append(value.Data[:0], frame.data...)
	value.PTS = pts
	value.Duration = duration
	return packet, nil
}

// Close implements mrtp.Element.
func (s *IVFSource) Close() error {
	return s.reader.Close()
}

var _ mrtp.Puller[mrtp.EncodedFrame] = (*IVFSource)(nil)
