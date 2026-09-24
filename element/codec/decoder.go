//go:build cgo

package codec

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/codec"
	"github.com/mengelbart/mrtp/codec/avcodec"
	"github.com/mengelbart/mrtp/codec/vpx"
	"github.com/mengelbart/mrtp/pipeline"
)

// Decoder decodes coded frames into raw frames. The picture size is not on the
// wire, so the decoder negotiates its output once it has decoded a frame, and
// again whenever the picture changes.
type Decoder struct {
	d codec.Decoder

	codec  mrtp.Codec
	format mrtp.RawVideo
	pool   *pipeline.Pool[mrtp.RawFrame]
	down   mrtp.Sink[mrtp.RawFrame]
}

func NewDecoder(c mrtp.Codec) (*Decoder, error) {
	d := &Decoder{codec: c}
	switch c {
	case mrtp.H264:
		dec, err := avcodec.NewH264Decoder()
		if err != nil {
			return nil, fmt.Errorf("failed to create H264 decoder: %w", err)
		}
		d.d = dec
	case mrtp.VP8, mrtp.VP9:
		dec, err := vpx.NewDecoder(c)
		if err != nil {
			return nil, fmt.Errorf("failed to create VPX decoder: %w", err)
		}
		d.d = dec
	default:
		return nil, fmt.Errorf("unsupported codec: %v", c)
	}
	return d, nil
}

// Negotiate implements mrtp.Sink.
func (d *Decoder) Negotiate(f mrtp.Format) error {
	encoded, ok := f.(mrtp.EncodedVideo)
	if !ok {
		return fmt.Errorf("decoder takes encoded video, not %v", f)
	}
	if encoded.Codec != d.codec {
		return fmt.Errorf("decoder is configured for %v, not %v", d.codec, encoded.Codec)
	}
	return nil
}

// Format implements mrtp.Source. It is the zero picture until the first frame
// is decoded, which is when the real one is negotiated downstream.
func (d *Decoder) Format() mrtp.Format {
	return d.format
}

// Connect implements mrtp.Source.
func (d *Decoder) Connect(down mrtp.Sink[mrtp.RawFrame]) error {
	if d.down != nil {
		return errors.New("codec: decoder is already connected")
	}
	d.down = down
	return nil
}

// Write implements mrtp.Sink.
func (d *Decoder) Write(packet mrtp.Packet[mrtp.EncodedFrame]) error {
	defer packet.Release()

	frame := packet.Value()

	decoded, err := d.d.Decode(frame.Data)
	if err != nil {
		return fmt.Errorf("failed to decode frame: %w", err)
	}

	slog.Info("decoder src", "length", len(decoded.Data), "pts", frame.PTS.Microseconds())

	if err := d.reformat(decoded); err != nil {
		return err
	}

	out := d.pool.Get()
	value := out.Value()
	if len(decoded.Data) < len(value.Y)+len(value.Cb)+len(value.Cr) {
		out.Release()
		return fmt.Errorf("decoder: short frame: got %v bytes, want %v",
			len(decoded.Data), len(value.Y)+len(value.Cb)+len(value.Cr))
	}
	n := copy(value.Y, decoded.Data)
	n += copy(value.Cb, decoded.Data[n:])
	copy(value.Cr, decoded.Data[n:])
	value.PTS = frame.PTS
	value.Duration = frame.Duration

	return d.down.Write(out)
}

// reformat negotiates the picture downstream, and builds the pool it is drawn
// from, whenever the decoded picture differs from the current format.
func (d *Decoder) reformat(frame *codec.DecodedFrame) error {
	format := mrtp.RawVideo{
		Width:       uint(frame.Width),
		Height:      uint(frame.Height),
		Subsampling: frame.ChromaSubsampling,
		// the wire carries no frame rate, so the sink picks its own
		FrameRate: d.format.FrameRate,
	}
	if format == d.format {
		return nil
	}
	ySize, cSize, err := format.PlaneSizes()
	if err != nil {
		return err
	}
	d.format = format
	d.pool = pipeline.NewPool(func() *mrtp.RawFrame {
		buffer := make([]byte, ySize+2*cSize)
		return &mrtp.RawFrame{
			Y:  buffer[:ySize],
			Cb: buffer[ySize : ySize+cSize],
			Cr: buffer[ySize+cSize:],
		}
	}, nil)
	return d.down.Negotiate(format)
}

// EndOfStream implements mrtp.Sink.
func (d *Decoder) EndOfStream() error {
	return d.down.EndOfStream()
}

// Close implements mrtp.Element.
func (d *Decoder) Close() error {
	return d.d.Close()
}

var (
	_ mrtp.Sink[mrtp.EncodedFrame] = (*Decoder)(nil)
	_ mrtp.Source[mrtp.RawFrame]   = (*Decoder)(nil)
)
