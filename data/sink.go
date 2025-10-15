package data

import (
	"io"
	"log/slog"
)

// DataSink currently only a noop sink
type DataSink struct {
	rc io.ReadCloser
}

// NewSink creates a new data sink. rc is the ReadCloser that provides the data to be consumed.
func NewSink(rc io.ReadCloser) (*DataSink, error) {
	d := &DataSink{
		rc: rc,
	}
	return d, nil
}

func (d *DataSink) Run() error {
	buf := make([]byte, 1024)
	for {
		n, err := d.rc.Read(buf)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		slog.Info("DataSink received data", "payload-length", n)
	}
}
