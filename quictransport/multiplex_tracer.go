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
	qlogProducer := t.qlogger.AddProducer()
	t.recorder = newRecorder(qlogProducer, t.quicCallback)

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

	r.qlogRecorder.RecordEvent(event)
}

func (r *Recoder) Close() error {
	return r.qlogRecorder.Close()
}
