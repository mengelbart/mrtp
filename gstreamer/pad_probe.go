package gstreamer

import (
	"log/slog"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp/logging"
)

func getRTPLogPadProbe(vantagePointName string) func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
	return func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
		buffer := ppi.GetBuffer()
		mapinfo := buffer.Map(gst.MapRead)
		defer buffer.Unmap()
		pkt := mapinfo.AsUint8Slice()
		err := logging.LogRTPpacket(pkt, vantagePointName)
		if err != nil {
			slog.Warn("pad probe failed to unmarshal RTP packet", "error", err)
			return gst.PadProbeOK
		}

		return gst.PadProbeOK
	}
}
