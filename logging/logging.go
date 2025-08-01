package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
)

type Format string

const (
	TextFormat Format = "text"
	JSONFormat Format = "json"
)

func Configure(format Format, level slog.Level, writer io.Writer) {
	if writer == nil {
		writer = os.Stderr
	}
	ho := &slog.HandlerOptions{
		AddSource:   false,
		Level:       level,
		ReplaceAttr: nil,
	}
	switch format {
	case JSONFormat:
		slog.SetDefault(slog.New(slog.NewJSONHandler(writer, ho)))
	case TextFormat:
		slog.SetDefault(slog.New(slog.NewTextHandler(writer, ho)))
	default:
		panic(fmt.Sprintf("unexpected logging.format: %#v", format))
	}
}

type RTPLogger struct {
	logger *slog.Logger
}

func NewRTPLogger(vantagePoint string, logger *slog.Logger) *RTPLogger {
	if logger == nil {
		logger = slog.Default().With("vantage-point", vantagePoint).WithGroup("rtp-packet")
	}
	return &RTPLogger{
		logger: logger,
	}
}

func (l *RTPLogger) LogRTPPacket(header *rtp.Header, payload []byte, _ interceptor.Attributes) {
	l.logger.Info(
		"rtp packet",
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
