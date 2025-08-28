package data

import (
	"io"
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
	for {
		buf := make([]byte, 4096)
		_, err := d.rc.Read(buf)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		// do nothing with the data
	}
}
