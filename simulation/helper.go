package simulation

import (
	"time"

	"github.com/mengelbart/netsim"
)

type pathFactory func() []netsim.Node

func pathFactoryFunc(delay time.Duration, bandwidth float64, burst, queueSize int, headDrop bool) pathFactory {
	return func() []netsim.Node {
		nodes := []netsim.Node{}
		if delay > 0 {
			nodes = append(nodes, netsim.NewQueueNode(netsim.NewDelayQueue(delay)))
		}
		if bandwidth > 0 {
			nodes = append(nodes,
				netsim.NewQueueNode(netsim.NewRateQueue(float64(bandwidth), burst, queueSize, headDrop)),
			)
		}
		return nodes
	}
}
