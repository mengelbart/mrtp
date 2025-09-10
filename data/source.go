package data

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math"
	"os"

	"golang.org/x/time/rate"
)

type DataBinOption func(*DataBin) error

type DataBin struct {
	useFileSrc bool
	filepath   string

	wc          io.WriteCloser
	rateLimiter *rate.Limiter
}

func DataBinUseFileSource(filepath string) DataBinOption {
	return func(d *DataBin) error {
		d.useFileSrc = true
		d.filepath = filepath
		return nil
	}
}

// DataBinUseRateLimiter: initLimit in bps, burst in bytes
func DataBinUseRateLimiter(initLimit, burst uint) DataBinOption {
	return func(d *DataBin) error {
		initLimitToBytes := bitRateToBytesPerSec(initLimit)

		d.rateLimiter = rate.NewLimiter(rate.Limit(initLimitToBytes), int(burst))
		return nil
	}
}

// NewDataBin creates a new data source. wc is the WriteCloser where data will be written to.
func NewDataBin(wc io.WriteCloser, options ...DataBinOption) (*DataBin, error) {
	d := &DataBin{
		useFileSrc: false,
		filepath:   "",
		wc:         wc,
	}
	for _, opt := range options {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *DataBin) SetRateLimit(ratebps uint) {
	if d.rateLimiter != nil {
		slog.Info("NEW_DATA_RATE", "rate", ratebps)

		rateBytes := bitRateToBytesPerSec(ratebps)
		d.rateLimiter.SetLimit(rate.Limit(rateBytes))
	}
}

func bitRateToBytesPerSec(bitrate uint) float64 {
	return math.Max(float64(bitrate)/8.0, 1)
}

func (d *DataBin) startFileSource() error {
	if d.wc == nil {
		return fmt.Errorf("data sink not set")
	}

	file, err := os.Open(d.filepath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	buf := make([]byte, 1024)
	for {
		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(context.TODO(), 1024)
			if err != nil {
				log.Fatal(err)
			}
		}

		n, readErr := file.Read(buf)
		if n > 0 {
			_, writeErr := d.wc.Write(buf[:n])
			if writeErr != nil {
				return fmt.Errorf("failed to write to sink: %w", writeErr)
			}

			logDataEvent(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read from file: %w", readErr)
		}
	}

	return nil
}

func (d *DataBin) startRandomSource() error {
	if d.wc == nil {
		return fmt.Errorf("data sink not set")
	}

	buf := make([]byte, 1024)
	rand.Read(buf)

	for {
		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(context.TODO(), 1024)
			if err != nil {
				log.Fatal(err)
			}
		}

		n, err := d.wc.Write(buf)
		if err != nil {
			return err
		}
		logDataEvent(n)
	}
}

func (d *DataBin) Run() error {
	if d.useFileSrc {
		err := d.startFileSource()
		if err != nil {
			return err
		}
	}

	return d.startRandomSource()
}

func logDataEvent(len int) {
	slog.Info("DataSource sent data", "payload-length", len)
}
