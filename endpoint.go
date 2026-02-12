package mrtp

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Endpoint manages a collection of sources and sinks.
type Endpoint struct {
	ctx       context.Context
	cancelCtx context.CancelFunc
	flows     []*Flow
}

func NewEndpoint() *Endpoint {
	ctx, cancel := context.WithCancel(context.Background())
	return &Endpoint{
		ctx:       ctx,
		cancelCtx: cancel,
		flows:     []*Flow{},
	}
}

func (e *Endpoint) AddFlow(f *Flow) {
	e.flows = append(e.flows, f)
}

func (e *Endpoint) Run() error {
	buf := make([]byte, 128_000)
	for {
		select {
		case <-e.ctx.Done():
			return nil
		default:
			n, err := e.flows[0].Input.Read(buf)
			if err != nil {
				var psr PeriodicSourceError
				if errors.As(err, &psr) {
					slog.Info("waiting for frame")
					time.Sleep(time.Until(psr.nextAvailable))
					break
				}
				return err
			}
			slog.Info("got frame")
			if e.flows[0].Containerizer != nil {
				var pkts [][]byte
				pkts, err = e.flows[0].Containerizer.Containerize(buf[:n])
				if err != nil {
					return err
				}
				for _, pkt := range pkts {
					if _, err = e.flows[0].Output.Write(pkt); err != nil {
						return err
					}
				}
			} else {
				if _, err = e.flows[0].Output.Write(buf[:n]); err != nil {
					return err
				}
			}
		}
	}
}

func (e *Endpoint) Close() error {
	e.cancelCtx()
	return nil
}
