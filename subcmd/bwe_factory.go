package subcmd

import (
	"time"

	"github.com/mengelbart/mrtp"
)

type BWEConfig struct {
	initTargetRate uint
	minTargetRate  uint
	maxTargetRate  uint
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
		return mrtp.NewNada(config.initTargetRate, config.minTargetRate, config.maxTargetRate, 20*time.Millisecond), nil
	}),
	"gcc": BWEFactoryFunc(func(config BWEConfig) (mrtp.BWE, error) {
		return mrtp.NewGCC(config.initTargetRate, config.minTargetRate, config.maxTargetRate)
	}),
}
