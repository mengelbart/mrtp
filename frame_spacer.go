package mrtp

import (
	"log/slog"
	"time"
)

type packets struct {
	payloads   [][]byte
	attributes Attributes
}

type FrameSpacer struct {
	writer        Writer
	frameDuration time.Duration
	pktChan       chan packets
}

func (p *FrameSpacer) Link(w Writer, i Info) (Writer, error) {
	p.pktChan = make(chan packets, 1000000)
	p.writer = w
	fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
	p.frameDuration = time.Duration(float64(time.Second) / fps)
	go p.run()
	return p, nil
}

func (p *FrameSpacer) Write(pkt []byte, attr Attributes) error {
	return p.writer.Write(pkt, attr)
}

func (p *FrameSpacer) WriteAll(pkts [][]byte, attr Attributes) error {
	slog.Info("spacer got packets", "count", len(pkts))
	p.pktChan <- packets{
		payloads:   pkts,
		attributes: attr,
	}
	return nil
}

func (p *FrameSpacer) run() {
	for pkts := range p.pktChan {
		if len(p.pktChan) > 2 {
			for _, pkt := range pkts.payloads {
				if err := p.writer.Write(pkt, pkts.attributes); err != nil {
					slog.Error("failed to send packet", "error", err)
				}
			}
			continue
		}
		space := 0.3 * float64(p.frameDuration.Microseconds()) / float64(len(pkts.payloads))
		spaceTime := time.Duration(space) * time.Microsecond
		slog.Info("pacing frame", "count", len(pkts.payloads), "space-time", spaceTime, "queue", len(p.pktChan))
		ticker := time.NewTicker(spaceTime)
		defer ticker.Stop()
		var next []byte
		for range ticker.C {
			if len(pkts.payloads) == 0 {
				break
			}
			next, pkts.payloads = pkts.payloads[0], pkts.payloads[1:]
			if err := p.writer.Write(next, pkts.attributes); err != nil {
				slog.Error("failed to send packet", "error", err)
			}
		}
	}
}
