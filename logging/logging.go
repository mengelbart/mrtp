package logging

import (
	"log/slog"
	"os"

	"github.com/pion/rtp"
)

func UseFileForLogging(file *os.File) {
	// set slog to use log file & to use json format
	slog.SetDefault(slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{})))
}

func LogRTPpacket(buf []uint8, vantagePointName string) error {
	b := rtp.Packet{}
	if err := b.Unmarshal(buf); err != nil {
		return err
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
		"payload-length", b.MarshalSize(),
	)

	return nil
}
