package gstreamer

import (
	"log/slog"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp/internal/logging"
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

func getRTPtoPTSMappingProbe(eventName string) func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
	return func(pad *gst.Pad, info *gst.PadProbeInfo) gst.PadProbeReturn {
		buffer := info.GetBuffer()
		if buffer != nil {
			// Get PTS from buffer metadata
			pts := buffer.PresentationTimestamp().AsDuration()
			offset := buffer.Offset()

			var ptsMs int64
			if pts != nil {
				ptsMs = pts.Milliseconds()
			}

			// Parse RTP packet to get RTP timestamp
			mapinfo := buffer.Map(gst.MapRead)
			defer buffer.Unmap()
			pkt := mapinfo.AsUint8Slice()

			if len(pkt) >= 12 {
				// Extract RTP timestamp
				rtpTimestamp := uint32(pkt[4])<<24 | uint32(pkt[5])<<16 | uint32(pkt[6])<<8 | uint32(pkt[7])

				slog.Info(eventName,
					"rtp-timestamp", rtpTimestamp,
					"pts", ptsMs,
					"offset", offset)
			}
		}
		return gst.PadProbeOK
	}
}

func getFrameProbe(eventName string) func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
	frameCount := uint64(0)
	return func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
		buffer := ppi.GetBuffer()
		if buffer != nil {
			pts := buffer.PresentationTimestamp().AsDuration()
			dts := buffer.DecodingTimestamp().AsDuration()
			duration := buffer.Duration().AsDuration()
			offset := buffer.Offset()

			// Convert to milliseconds
			var ptsMs, dtsMs, durationMs int64
			if pts != nil {
				ptsMs = pts.Milliseconds()
			}
			if dts != nil {
				dtsMs = dts.Milliseconds()
			}
			if duration != nil {
				durationMs = duration.Milliseconds()
			}

			slog.Info(eventName, "pts", ptsMs, "dts", dtsMs, "duration", durationMs, "offset", offset, "frame-count", frameCount)
			frameCount++
		}
		return gst.PadProbeOK
	}
}
