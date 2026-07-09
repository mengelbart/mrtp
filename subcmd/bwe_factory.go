package subcmd

import (
	"flag"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/mengelbart/mrtp"
)

const (
	initTargetRate = 1_000_000
	minTargetRate  = 400_000
)

type BWEConfig struct {
	InitTargetRate uint
	MinTargetRate  uint
	MaxTargetRate  uint
}

type BWEFactory interface {
	MakeBWE(BWEConfig) (mrtp.BWE, error)
}

type BWEFactoryFunc func(BWEConfig) (mrtp.BWE, error)

func (f BWEFactoryFunc) MakeBWE(config BWEConfig) (mrtp.BWE, error) {
	return f(config)
}

var BWEFactories = map[string]BWEFactory{
	"nada": BWEFactoryFunc(func(config BWEConfig) (mrtp.BWE, error) {
		nadaConfig := nada.Config{
			MinRate:                  uint64(config.MinTargetRate),
			MaxRate:                  uint64(config.MaxTargetRate),
			StartRate:                uint64(config.InitTargetRate),
			FeedbackDelta:            uint64(20 / time.Millisecond), // convert to ms
			DeactivateQDelayWrapping: true,

			RefCongLevel:           DefaultBweFlags.RefCongLevel,
			QEPS:                   DefaultBweFlags.QEPS,
			MaxRampUpFactor:        DefaultBweFlags.MaxRampUpFactor,
			MaxGradualUpdateFactor: DefaultBweFlags.MaxGradualUpdateFactor,
			Kappa:                  DefaultBweFlags.Kappa,
			Eta:                    DefaultBweFlags.Eta,
			DFILT:                  DefaultBweFlags.DFILT,
			QBOUND:                 DefaultBweFlags.QBOUND,
		}
		return mrtp.NewNadaWithConfig(nadaConfig), nil
	}),
	"gcc": BWEFactoryFunc(func(config BWEConfig) (mrtp.BWE, error) {
		return mrtp.NewGCC(config.InitTargetRate, config.MinTargetRate, config.MaxTargetRate)
	}),
}

type bweFlags struct {
	RefCongLevel           uint64  // target congestion level in ms
	QEPS                   uint64  // Threshold for determining queuing delay buildup at receiver
	MaxRampUpFactor        float64 // Upper bound on rate increase ratio for accelerated ramp up
	MaxGradualUpdateFactor float64 // Upper bound on rate increase ratio for gradual updates (decrease not limited)
	Kappa                  float64 // tuning of gradual update mode
	Eta                    float64 // tuning of gradual update mode
	DFILT                  uint64  // tuning of rampUp: Bound on filtering delay for RampUp Mode
	QBOUND                 uint64  // tuning of rampUp: Upper bound on self-inflicted queuing delay during ramp up
}

func (b *bweFlags) ConfigureFlags(fs *flag.FlagSet) {
	fs.Uint64Var(&b.RefCongLevel, "nada-ref-cong-level", nada.XREF, "Reference congestion level in ms")
	fs.Uint64Var(&b.QEPS, "nada-qeps", nada.QEPS, "Threshold for determining queuing delay buildup at receiver in ms")
	fs.Float64Var(&b.MaxRampUpFactor, "nada-max-ramp-up-factor", nada.GAMMA_MAX, "Upper bound on rate increase ratio for accelerated ramp up")
	fs.Float64Var(&b.MaxGradualUpdateFactor, "nada-max-gradual-update-factor", nada.GRAD_UPDATE_FACTOR, "Upper bound on rate increase ratio for gradual updates (decrease not limited)")
	fs.Float64Var(&b.Kappa, "nada-kappa", nada.KAPPA, "Scaling parameter for gradual rate update calculation")
	fs.Float64Var(&b.Eta, "nada-eta", nada.ETA, "Scaling parameter for gradual rate update calculation")
	fs.Uint64Var(&b.DFILT, "nada-dfilt", nada.DFILT, "Bound on filtering delay for RampUp Mode in ms")
	fs.Uint64Var(&b.QBOUND, "nada-qbound", nada.QBOUND, "Upper bound on self-inflicted queuing delay during ramp up in ms")
}

var DefaultBweFlags *bweFlags = &bweFlags{}
