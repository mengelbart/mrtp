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
					logRTPpacket(buffer, logger)
					return true
				})
			}
		}
		if (ppi.Type() & gst.PadProbeTypeBuffer) > 0 {
			buffer := ppi.GetBuffer()
			if buffer != nil {
				logRTPpacket(buffer, logger)
			}
		}
		return gst.PadProbeOK
	}
}

func getRTPtoPTSMappingProbe(eventName string) func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
	unwrapper := &logging.Unwrapper{}
	return func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
		if (ppi.Type() & gst.PadProbeTypeBufferList) > 0 {
			list := ppi.GetBufferList()
			if list != nil {
				list.ForEach(func(buffer *gst.Buffer, idx uint) bool {
					logRTPMapping(eventName, buffer, unwrapper)
					return true
				})
			}
		}
		if (ppi.Type() & gst.PadProbeTypeBuffer) > 0 {
			buffer := ppi.GetBuffer()
			if buffer != nil {
				logRTPMapping(eventName, buffer, unwrapper)
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
			lenght := buffer.GetSize()

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

			slog.Debug(eventName, "pts", ptsMs, "dts", dtsMs, "duration", durationMs, "offset", offset, "frame-count", frameCount, "length", lenght)
			frameCount++
		}
		return gst.PadProbeOK
	}
}

func logRTPpacket(buffer *gst.Buffer, logger *logging.RTPLogger) {
	mapinfo := buffer.Map(gst.MapRead)
	defer buffer.Unmap()
	pkt := mapinfo.AsUint8Slice()
	b := rtp.Packet{}
	if err := b.Unmarshal(pkt); err != nil {
		return
	}
	logger.LogRTPPacket(&b.Header, b.Payload, nil)
}

func logRTPMapping(eventName string, buffer *gst.Buffer, unwrapper *logging.Unwrapper) {
	mapinfo := buffer.Map(gst.MapRead)
	defer buffer.Unmap()
	pkt := mapinfo.AsUint8Slice()
	b := rtp.Packet{}
	if err := b.Unmarshal(pkt); err != nil {
		return
	}
	// Get PTS from buffer metadata
	pts := buffer.PresentationTimestamp().AsDuration()
	offset := buffer.Offset()

	var ptsMs int64
	if pts != nil {
		ptsMs = pts.Milliseconds()
	}

	slog.Debug(eventName,
		"rtp-timestamp", b.Header.Timestamp,
		"sequence-number", b.Header.SequenceNumber,
		"unwrapped-sequence-number", unwrapper.Unwrap(b.Header.SequenceNumber),
		"pts", ptsMs,
		"offset", offset)
}
