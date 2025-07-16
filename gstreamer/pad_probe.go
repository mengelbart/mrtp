package gstreamer

import (
	"log/slog"

	"github.com/go-gst/go-gst/gst"
	"github.com/pion/rtp"
)

func getRTPLogPadProbe(vantagePointName string) func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
	return func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
		buffer := ppi.GetBuffer()
		mapinfo := buffer.Map(gst.MapRead)
		defer buffer.Unmap()
		pkt := mapinfo.AsUint8Slice()
		b := rtp.Packet{}
		if err := b.Unmarshal(pkt); err != nil {
			slog.Warn("pad probe failed to unmarshal RTP packet", "error", err)
			return gst.PadProbeOK
		}
		slog.Info(
			"pad probe received RTP packet",
			"vantage-point", vantagePointName,
			"version", b.Version,
			"padding", b.Padding,
			"marker", b.Marker,
			"payload-type", b.PayloadType,
			"sequence-number", b.SequenceNumber,
			"timestamp", b.Timestamp,
			"ssrc", b.SSRC,
			"payload-length", len(b.Payload),
		)
		return gst.PadProbeOK
	}
}
