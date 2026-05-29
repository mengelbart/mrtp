package quictransport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
)

type tracerFactory struct {
	qlogLabel string
	transport *Transport
}

func (f *tracerFactory) newTracer(ctx context.Context, isClient bool, connID qlogwriter.ConnectionID) qlogwriter.Trace {
	var qfs *qlogwriter.FileSeq
	if len(f.qlogLabel) > 0 {
		qfs = qlogTracer(isClient, connID, f.qlogLabel, nil)
	}
	return &tracer{
		qlogFileSeq: qfs,
		transport:   f.transport,
		baseTime:    time.Now(),
	}
}

type multiplexedRecorder struct {
	recorders []qlogwriter.Recorder
}

func (r *multiplexedRecorder) RecordEvent(event qlogwriter.Event) {
	for _, recorder := range r.recorders {
		recorder.RecordEvent(event)
	}
}

func (r *multiplexedRecorder) Close() error {
	var errs []error
	for _, recorder := range r.recorders {
		if err := recorder.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type tracer struct {
	qlogFileSeq *qlogwriter.FileSeq
	transport   *Transport
	baseTime    time.Time
}

func (t *tracer) AddProducer() qlogwriter.Recorder {
	recorders := []qlogwriter.Recorder{}
	if t.qlogFileSeq != nil {
		recorders = append(recorders, t.qlogFileSeq.AddProducer())
	}
	recorders = append(recorders, &traceWriter{t: t})
	return &multiplexedRecorder{recorders: recorders}
}

func (t *tracer) SupportsSchemas(schema string) bool {
	return true
}

func (t *tracer) record(ts time.Time, event qlogwriter.Event) {
	// TODO: Listen for relevant events
	switch e := event.(type) {
	case qlog.PacketReceived:
		for _, frame := range e.Frames {
			switch f := frame.Frame.(type) {
			case *qlog.AckFrame:
				previous := time.Time{}
				for _, tsRange := range f.ReceiveTimestamps {
					for j, delta := range tsRange.TimestampDelta {
						seqNr := uint64(f.LargestAcked()) - uint64(j)
						delta := time.Duration(delta) * time.Microsecond
						var arrival time.Time
						if previous.IsZero() {
							arrival = t.baseTime.Add(delta)
						} else {
							arrival = previous.Add(-delta)
						}
						previous = arrival
						t.transport.packetAcked(seqNr, arrival)
					}
				}
				t.transport.updateECNCounts(f.ECT0, f.ECT1, f.ECNCE)
			}
		}
	case qlog.PacketSent:
		t.transport.packetSent(ts, uint64(e.Header.PacketNumber), e.Raw.Length)
	case qlog.PacketLost:
		t.transport.packetLost(uint64(e.Header.PacketNumber))
	}
	t.transport.updateCongestionControl()
}

type traceWriter struct {
	t *tracer
}

func (w *traceWriter) Close() error {
	return nil
}

func (w *traceWriter) RecordEvent(ev qlogwriter.Event) {
	w.t.record(time.Now(), ev)
}

// Everything below is mostly copy from qlog.DefaultConnectionTracer

func qlogTracer(isClient bool, connID qlogwriter.ConnectionID, label string, eventSchemas []string) *qlogwriter.FileSeq {
	qlogDir := os.Getenv("QLOGDIR")
	if qlogDir == "" {
		return nil
	}
	if _, err := os.Stat(qlogDir); os.IsNotExist(err) {
		if err := os.MkdirAll(qlogDir, 0o755); err != nil {
			log.Fatalf("failed to create qlog dir %s: %v", qlogDir, err)
		}
	}
	path := fmt.Sprintf("%s/%s_%s.sqlog", strings.TrimRight(qlogDir, "/"), connID, label)
	f, err := os.Create(path)
	if err != nil {
		log.Printf("Failed to create qlog file %s: %s", path, err.Error())
		return nil
	}
	fileSeq := qlogwriter.NewConnectionFileSeq(
		newBufferedWriteCloser(bufio.NewWriter(f), f),
		isClient,
		connID,
		eventSchemas,
	)
	go fileSeq.Run()
	return fileSeq
}

type bufferedWriteCloser struct {
	*bufio.Writer
	io.Closer
}

// newBufferedWriteCloser creates an io.WriteCloser from a bufio.Writer and an io.Closer
func newBufferedWriteCloser(writer *bufio.Writer, closer io.Closer) io.WriteCloser {
	return &bufferedWriteCloser{
		Writer: writer,
		Closer: closer,
	}
}

func (h bufferedWriteCloser) Close() error {
	if err := h.Flush(); err != nil {
		return err
	}
	return h.Closer.Close()
}
