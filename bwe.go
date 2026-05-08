package mrtp

import (
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/pion/bwe/gcc"
	pion_logging "github.com/pion/logging"
)

type ECN uint8

const (
	ECNNonECT ECN = iota
	ECNECT1
	ECNECT0
	ECNCE
)

type BWE interface {
	OnAck(sequenceNumber uint64, size int, departure, arrival time.Time, ecn ECN)
	OnLoss(sequenceNumber uint64, size int, departure time.Time)
	UpdateRTT(rtt time.Duration)
	UpdateECNCounts(ect0, ect1, ce uint64)
	UpdateTargetRate(time.Time) int
}

var _ BWE = (*Nada)(nil)

type Nada struct {
	nada *nada.SenderOnly
}

func NewNada(initRate, minRate, maxRate uint, feedbackInterval time.Duration) *Nada {
	nadaConfig := nada.Config{
		MinRate:                  uint64(minRate),
		MaxRate:                  uint64(maxRate),
		StartRate:                uint64(initRate),
		FeedbackDelta:            uint64(feedbackInterval / time.Millisecond), // convert to ms
		DeactivateQDelayWrapping: true,
		RefCongLevel:             15, // ms
	}
	nada := nada.NewSenderOnly(nadaConfig)
	return &Nada{
		nada: &nada,
	}
}

// OnAck implements [BWE].
func (n *Nada) OnAck(sequenceNumber uint64, size int, departure time.Time, arrival time.Time, ecn ECN) {
	n.nada.OnAck(sequenceNumber, departure, arrival, uint64(8*size), ecn == ECNCE)
}

// OnLoss implements [BWE].
func (n *Nada) OnLoss(sequenceNumber uint64, size int, departure time.Time) {
	n.nada.OnLoss(sequenceNumber, departure)
}

// UpdateECNCounts implements [BWE].
func (n *Nada) UpdateECNCounts(ect0 uint64, ect1 uint64, ce uint64) {
	// nada doesn't care about aggregated ECN counts
}

// UpdateRTT implements [BWE].
func (n *Nada) UpdateRTT(rtt time.Duration) {
	n.nada.UpdateRTT(rtt)
}

// UpdateTargetRate implements [BWE].
func (n *Nada) UpdateTargetRate(time.Time) int {
	return int(n.nada.UpdateTargetRate())
}

var _ BWE = (*GCC)(nil)

type GCC struct {
	gcc     *gcc.SendSideController
	lastRTT time.Duration
}

func NewGCC(initialRate, minRate, maxRate uint) (*GCC, error) {
	plf := pion_logging.NewJSONLoggerFactory()
	gcc, err := gcc.NewSendSideController(int(initialRate), int(minRate), int(maxRate), gcc.WithLoggerFactory(plf))
	if err != nil {
		return nil, err
	}
	return &GCC{
		gcc: gcc,
	}, nil
}

// OnAck implements [BWE].
func (g *GCC) OnAck(sequenceNumber uint64, size int, departure time.Time, arrival time.Time, _ ECN) {
	g.gcc.OnAck(sequenceNumber, size, departure, arrival)
}

// OnLoss implements [BWE].
func (g *GCC) OnLoss(uint64, int, time.Time) {
	g.gcc.OnLoss()
}

// UpdateECNCounts implements [BWE].
func (g *GCC) UpdateECNCounts(uint64, uint64, uint64) {
	// gcc doesn't care about aggregated ECN counts
}

// UpdateRTT implements [BWE].
func (g *GCC) UpdateRTT(rtt time.Duration) {
	g.lastRTT = rtt
}

// UpdateTargetRate implements [BWE].
func (g *GCC) UpdateTargetRate(time time.Time) int {
	return g.gcc.OnFeedback(time, g.lastRTT)
}
