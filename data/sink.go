package data

import (
	"encoding/binary"
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

func (d *DataSink) read(buf []byte, currentChunk int) (int, error) {
	n, err := d.rc.Read(buf)
	if err != nil {
		slog.Info("Datasink error: ", "err", err)
		return n, err
	}
	slog.Info("DataSink received data", "payload-length", n, "chunk-number", currentChunk)
	return n, nil
}

func (d *DataSink) Run() error {
	slog.Info("DataSink started")

	currentChunk := 0
	for {
		// Read chunk size header first (8 bytes for uint64)
		headerBuf := make([]byte, 8)
		n, err := d.read(headerBuf, 0)
		if err != nil {
			return err
		}
		if n < 8 {
			slog.Error("DataSink failed to read full header", "bytes-read", n)
			return io.ErrUnexpectedEOF
		}

		chunkSize := binary.BigEndian.Uint64(headerBuf)

		slog.Info("DataSink Chunk started", "chunk-number", currentChunk, "chunk-size", chunkSize)

		if chunkSize == 0 { // only one chunk
			slog.Info("DataSink chunksize 0")
			buf := make([]byte, 2048)
			for {
				_, err = d.read(buf, currentChunk)
				if err != nil {
					return err
				}
			}
		} else {
			read := 0

			buf := make([]byte, 2048)
			for read < int(chunkSize) {
				n, err := d.read(buf, currentChunk)
				if err != nil {
					return err
				}
				read += n
			}
			slog.Info("DataSink Chunk finished", "chunk-number", currentChunk)
			currentChunk++
		}
	}
}
