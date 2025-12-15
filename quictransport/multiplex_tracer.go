package quictransport

import (
	"github.com/quic-go/quic-go/qlogwriter"
)

type QuicEventCallback func(qlogwriter.Event)

// MultiplexTracer multiplexes qlog tracing and custom callbacks.
type MultiplexTracer struct {
	recorder     *Recoder
	qlogger      qlogwriter.Trace
	quicCallback QuicEventCallback
}

func newTracer(qlogger qlogwriter.Trace, quicCallback QuicEventCallback) *MultiplexTracer {
	return &MultiplexTracer{
		quicCallback: quicCallback,
		qlogger:      qlogger,
	}
}

func (t *MultiplexTracer) AddProducer() qlogwriter.Recorder {
	if t.qlogger != nil {
		qlogProducer := t.qlogger.AddProducer()
		t.recorder = newRecorder(qlogProducer, t.quicCallback)
	} else {
		t.recorder = newRecorder(nil, t.quicCallback)
	}

	return t.recorder
}

func (t *MultiplexTracer) SupportsSchemas(schema string) bool {
	return t.qlogger.SupportsSchemas(schema)
}

type Recoder struct {
	qlogRecorder qlogwriter.Recorder
	quicCallback QuicEventCallback
}

func newRecorder(qlogRecorder qlogwriter.Recorder, quicCallback QuicEventCallback) *Recoder {
	return &Recoder{
		quicCallback: quicCallback,
		qlogRecorder: qlogRecorder,
	}
}

func (r *Recoder) RecordEvent(event qlogwriter.Event) {
	if r.quicCallback != nil {
		r.quicCallback(event)
	}
	if r.qlogRecorder != nil {
		r.qlogRecorder.RecordEvent(event)
	}
}

func (r *Recoder) Close() error {
	if r.qlogRecorder == nil {
		return nil
	}
	return r.qlogRecorder.Close()
}
