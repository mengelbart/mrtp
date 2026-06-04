package subcmd

import (
	"time"

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
		return mrtp.NewNada(config.InitTargetRate, config.MinTargetRate, config.MaxTargetRate, 20*time.Millisecond), nil
	}),
	"gcc": BWEFactoryFunc(func(config BWEConfig) (mrtp.BWE, error) {
		return mrtp.NewGCC(config.InitTargetRate, config.MinTargetRate, config.MaxTargetRate)
	}),
}
