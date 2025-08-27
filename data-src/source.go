package datasrc

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log"
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

func NewDataBin(options ...DataBinOption) (*DataBin, error) {
	d := &DataBin{
		useFileSrc: false,
		filepath:   "",
	}
	for _, opt := range options {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *DataBin) AddDataTransportSink(wc io.WriteCloser) {
	d.wc = wc
}

func (d *DataBin) SetRateLimit(ratebps uint) {
	if d.rateLimiter != nil {
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

	buf := make([]byte, 1028)
	for {
		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(context.TODO(), 1028)
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

	buf := make([]byte, 4096)
	for {
		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(context.TODO(), 4096)
			if err != nil {
				log.Fatal(err)
			}
		}

		rand.Read(buf)
		_, err := d.wc.Write(buf)
		if err != nil {
			return err
		}
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
