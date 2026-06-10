//go:build go1.26 && simulation

package simulation

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/netsim"
)

const (
	rtpFlowID         = 0
	rtcpRecvFlowID    = 1
	rtcpSendFlowID    = 2
	dataChannelFlowID = 3
)

func initTestResultDir(t *testing.T) error {
	return os.MkdirAll(t.ArtifactDir(), 0755)
}

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

func configureLogging(t *testing.T) *os.File {
	os.Setenv("QLOGDIR", t.ArtifactDir())

	f, err := os.Create(filepath.Join(t.ArtifactDir(), "sim.stderr.log"))
	if err != nil {
		fmt.Printf("failed to open log file: %v\n", err)
		os.Exit(1)
	}

	logging.Configure(logging.Format(logging.JSONFormat), slog.Level(0), f)
	return f
}

func createFakeConfig(t *testing.T, testName string) error {
	config := `{"name": "simulation_` + testName + `","applications": [{"name": "receiver","namespace": "ns1"},{"name": "sender","namespace": "ns4"}],"duration": 100,"time": "2000-01-01T01:00:00.01+01:00"}` + "\n"
	return os.WriteFile(filepath.Join(t.ArtifactDir(), "config.json"), []byte(config), 0o644)
}
