package simulation

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/mengelbart/mrtp/internal/logging"
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

func configureLogging() *os.File {
	f, err := os.Create("./stderr.log")
	if err != nil {
		fmt.Printf("failed to open log file: %v\n", err)
		os.Exit(1)
	}

	logging.Configure(logging.Format(logging.JSONFormat), slog.Level(0), f)
	return f
}

func createFakeConfig(filePath string) error {
	const config = `{"name": "quic-test","applications": [{"name": "receiver","namespace": "ns1"},{"name": "sender","namespace": "ns4"}],"duration": 100,"time": "2000-01-01T01:00:00.01+01:00"}` + "\n"
	return os.WriteFile(filePath, []byte(config), 0o644)
}
