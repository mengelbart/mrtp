package logging

import (
	"log/slog"
	"os"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

func UseFileForLogging(file *os.File) {
	// set slog to use log file & to use json format
	slog.SetDefault(slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{})))
}

type RTPLogger struct {
	logger           *slog.Logger
	vantagePointName string
}

func NewRTPLogger(vantagePoint string, logger *slog.Logger) *RTPLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &RTPLogger{
		logger:           logger,
		vantagePointName: vantagePoint,
	}
}

func (l *RTPLogger) LogRTPPacket(header *rtp.Header, payload []byte, _ interceptor.Attributes) {
	l.logger.Info(
		"pad probe received RTP packet",
		"vantage-point", l.vantagePointName,
		"version", header.Version,
		"padding", header.Padding,
		"marker", header.Marker,
		"payload-type", header.PayloadType,
		"sequence-number", header.SequenceNumber,
		"timestamp", header.Timestamp,
		"ssrc", header.SSRC,
		"payload-length", header.MarshalSize()+len(payload),
	)
}

func (l *RTPLogger) LogRTCPPackets([]rtcp.Packet, interceptor.Attributes) {}
