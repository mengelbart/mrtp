package gstreamer

import (
	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp/logging"
	"github.com/pion/rtp"
)

func getRTPLogPadProbe(vantagePointName string) func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
	logger := logging.NewRTPLogger(vantagePointName, nil)
	return func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
		if (ppi.Type() & gst.PadProbeTypeBufferList) > 0 {
			list := ppi.GetBufferList()
			if list != nil {
				list.ForEach(func(buffer *gst.Buffer, idx uint) bool {
					mapinfo := buffer.Map(gst.MapRead)
					defer buffer.Unmap()
					pkt := mapinfo.AsUint8Slice()
					b := rtp.Packet{}
					if err := b.Unmarshal(pkt); err != nil {
						return true
					}
					logger.LogRTPPacket(&b.Header, b.Payload, nil)
					return true
				})
			}
		}
		if (ppi.Type() & gst.PadProbeTypeBuffer) > 0 {
			buffer := ppi.GetBuffer()
			if buffer != nil {
				mapinfo := buffer.Map(gst.MapRead)
				defer buffer.Unmap()
				pkt := mapinfo.AsUint8Slice()
				b := rtp.Packet{}
				if err := b.Unmarshal(pkt); err != nil {
					return gst.PadProbeOK
				}
				logger.LogRTPPacket(&b.Header, b.Payload, nil)
			}
		}
		return gst.PadProbeOK
	}
}
