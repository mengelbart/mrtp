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
