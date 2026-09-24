package webrtc

import (
	"errors"

	"github.com/mengelbart/mrtp"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/rtpfb"
	"github.com/pion/rtcp"
)

// rateController turns congestion control feedback into a target rate.
type rateController interface {
	// onFeedback returns the target rate in bits per second after report. A
	// non-positive rate leaves the current one unchanged.
	onFeedback(report rtpfb.Report) (float64, error)
}

// setRateController installs c as the transport's only congestion controller.
func (t *Transport) setRateController(c rateController) error {
	if t.rateController != nil {
		return errors.New("webrtc: a congestion controller is already set")
	}
	t.rateController = c
	return nil
}

// bweController feeds CCFB reports into an mrtp.BWE.
type bweController struct {
	bwe               mrtp.BWE
	ect0, ect1, ecnce uint64
}

func (c *bweController) onFeedback(report rtpfb.Report) (float64, error) {
	for _, p := range report.PacketReports {
		if !p.Arrived {
			c.bwe.OnLoss(p.SequenceNumber, p.Size, p.Departure)
			continue
		}
		c.bwe.OnAck(p.SequenceNumber, p.Size, p.Departure, p.Arrival, mrtp.ECN(p.ECN))
		switch p.ECN {
		case rtcp.ECNECT0:
			c.ect0++
		case rtcp.ECNECT1:
			c.ect1++
		case rtcp.ECNCE:
			c.ecnce++
		}
	}
	c.bwe.UpdateECNCounts(c.ect0, c.ect1, c.ecnce)
	c.bwe.UpdateRTT(report.RTT)
	return float64(c.bwe.UpdateTargetRate(report.Arrival)), nil
}

type screamFactory interface {
	interceptor.Factory
	GetTargetRate(id string, ssrc uint32) (float64, error)
}

// screamController reads the target rate SCReAM computed in its interceptor.
type screamController struct {
	factory   screamFactory
	transport *Transport
}

// onFeedback returns the target rate of the first SSRC in report, so it only
// supports a single local track.
func (c *screamController) onFeedback(report rtpfb.Report) (float64, error) {
	if len(report.PacketReports) == 0 || report.PacketReports[0].SSRC == 0 {
		return 0, nil
	}
	return c.factory.GetTargetRate(c.transport.pc.ID(), report.PacketReports[0].SSRC)
}
