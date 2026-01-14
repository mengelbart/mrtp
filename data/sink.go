package data

import (
	"io"
	"log/slog"

	"github.com/quic-go/quic-go/quicvarint"
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
	slog.Info("DataSink started")

	currentChunk := 0

	for {
		// Read chunk size header first
		headerBuf := make([]byte, 10)
		n, err := d.rc.Read(headerBuf)
		if err != nil {
			if err == io.EOF {
				slog.Info("DataSink finished")
				return nil
			}
			slog.Info("Datasink error: ", "err", err)
			return err
		}

		chunkSize, varintLen, err := quicvarint.Parse(headerBuf[:n])
		if err != nil {
			return err
		}

		slog.Info("DataSink Chunk started", "chunk-number", currentChunk, "chunk-size", chunkSize)

		if chunkSize == 0 { // only one chunk
			slog.Info("DataSink chunksize 0")
			buf := make([]byte, 2048)
			for {
				n, err := d.rc.Read(buf)
				if err != nil {
					if err == io.EOF {
						slog.Info("DataSink Chunk finished", "chunk-number", currentChunk)
						return nil
					}
					slog.Info("Datasink error: ", "err", err)
					return err
				}

				slog.Info("DataSink received data", "payload-length", n, "chunk-number", currentChunk)
			}
		} else {
			remainingFromHeader := n - varintLen
			read := remainingFromHeader

			buf := make([]byte, 2048)
			for read < int(chunkSize) {
				n, err := d.rc.Read(buf)
				if err != nil {
					if err == io.EOF {
						slog.Info("DataSink Chunk finished", "chunk-number", currentChunk)
						return nil
					}
					slog.Info("Datasink error: ", "err", err)
					return err
				}

				read += n
				slog.Info("DataSink received data", "payload-length", n, "chunk-number", currentChunk)
			}
			slog.Info("DataSink Chunk finished", "chunk-number", currentChunk)
			currentChunk++
		}
	}
}
