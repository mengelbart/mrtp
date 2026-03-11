package gopipe

import (
	"fmt"
	"log/slog"

	"github.com/mengelbart/mrtp/gopipe/codec"
)

type Decoder struct {
	x264dec *codec.H264Decoder
	vpxdec  *codec.VPXDecoder
}

func NewDecoder(c codec.CodecType) (*Decoder, error) {
	switch c {
	case codec.H264:
		dec, err := codec.NewH264Decoder()
		if err != nil {
			return nil, fmt.Errorf("failed to create H264 decoder: %w", err)
		}
		return &Decoder{
			x264dec: dec,
		}, nil
	case codec.VP8, codec.VP9:
		dec, err := codec.NewVPXDecoder(c)
		if err != nil {
			return nil, fmt.Errorf("failed to create H264 decoder: %w", err)
		}
		return &Decoder{
			vpxdec: dec,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported codec: %v", c)
	}
}

func (d *Decoder) Link(next Writer, i Info) (Writer, error) {
	return WriterFunc(func(encFrame []byte, attrs Attributes) error {
		pts, err := getPTS(attrs)
		if err != nil {
			return err
		}

		var decFrame *codec.DecodedFrame
		if d.x264dec != nil {
			decFrame, err = d.x264dec.Decode(encFrame)
		} else if d.vpxdec != nil {
			decFrame, err = d.vpxdec.Decode(encFrame)
		} else {
			return fmt.Errorf("no decoder available")
		}
		if err != nil {
			return fmt.Errorf("failed to decode frame: %w", err)
		}

		slog.Info("decoder src", "length", len(decFrame.Data), "pts", pts)

		// merge attributes
		if attrs == nil {
			attrs = make(Attributes)
		}

		attrs[Width] = decFrame.Width
		attrs[Height] = decFrame.Height
		attrs[ChromaSubsampling] = decFrame.ChromaSubsampling

		return next.Write(decFrame.Data, attrs)
	}), nil
}

func (d *Decoder) Close() error {
	if d.x264dec != nil {
		d.x264dec.Close()
	}
	if d.vpxdec != nil {
		d.vpxdec.Close()
	}
	return nil
}
