package mrtp_test

import (
	"log"
	"log/slog"
	"os"
	"testing"
	"testing/synctest"

	"github.com/mengelbart/mrtp/logging"
	"github.com/mengelbart/mrtp/webrtc"
	pionlogging "github.com/pion/logging"
	"github.com/pion/transport/v3/vnet"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
)

type testSignaler struct {
	peer  *webrtc.Transport
	block chan struct{}
}

func (s *testSignaler) SendSessionDescription(sd *pion.SessionDescription) error {
	<-s.block
	return s.peer.HandleSessionDescription(sd)
}

func (s *testSignaler) SendICECandidate(c *pion.ICECandidate) error {
	<-s.block
	return s.peer.HandleICECandidate(pion.ICECandidateInit{
		Candidate:        c.ToJSON().Candidate,
		SDPMid:           new(string),
		SDPMLineIndex:    new(uint16),
		UsernameFragment: new(string),
	})
}

func TestWebRTCSimulation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logging.Configure(logging.TextFormat, slog.Level(-9), os.Stdout)
		waitForDC := make(chan struct{})

		router, err := vnet.NewRouter(&vnet.RouterConfig{
			CIDR:          "0.0.0.0/0",
			LoggerFactory: pionlogging.NewDefaultLoggerFactory(),
		})
		assert.NoError(t, err)
		router.Start()

		leftNet, err := vnet.NewNet(&vnet.NetConfig{
			StaticIPs: []string{},
			StaticIP:  "10.0.0.1",
		})
		assert.NoError(t, err)

		err = router.AddNet(leftNet)
		assert.NoError(t, err)

		rightNet, err := vnet.NewNet(&vnet.NetConfig{
			StaticIPs: []string{},
			StaticIP:  "10.0.0.2",
		})
		assert.NoError(t, err)

		err = router.AddNet(rightNet)
		assert.NoError(t, err)

		sendSignaler := &testSignaler{
			block: make(chan struct{}),
		}

		sender, err := webrtc.NewTransport(
			sendSignaler,
			true,
			webrtc.SetVNet(leftNet),
		)
		assert.NoError(t, err)

		receiveSignaler := &testSignaler{
			block: make(chan struct{}),
		}
		receiver, err := webrtc.NewTransport(
			receiveSignaler,
			false,
			webrtc.SetVNet(rightNet),
			webrtc.OnDataChannel(func(dc *pion.DataChannel) {
				dc.OnOpen(func() {
					dc.OnMessage(func(msg pion.DataChannelMessage) {
						log.Printf("received message: %v", string(msg.Data))
					})
					close(waitForDC)
				})
			}),
		)
		assert.NoError(t, err)

		sendSignaler.peer = receiver
		receiveSignaler.peer = sender

		close(sendSignaler.block)
		close(receiveSignaler.block)

		dc, err := sender.CreateDataChannel("test")
		dc.OnOpen(func() {
			err = dc.Send([]byte("hello"))
			assert.NoError(t, err)
			log.Println("Message sent")
		})
		assert.NoError(t, err)
		assert.NotNil(t, dc)
		<-waitForDC
		log.Println("opened dc")

		synctest.Wait()

		sender.Close()
		receiver.Close()
		router.Stop()
	})
}
