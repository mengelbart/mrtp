package datasrc

import (
	"io"
)

// DataSink currently only a noop sink
type DataSink struct {
	rc io.ReadCloser
}

func NewSink() (*DataSink, error) {
	d := &DataSink{}
	return d, nil
}

func (d *DataSink) AddDataTransportSink(rc io.ReadCloser) {
	d.rc = rc
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
