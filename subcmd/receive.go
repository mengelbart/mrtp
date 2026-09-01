package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/roq"
	"github.com/mengelbart/mrtp/udp"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("receive", func() cmdmain.SubCmd { return new(Receive) })
}

type Receive struct {
	localAddr         string
	remoteAddr        string
	roqMapping        uint
	roqServer         bool
	roqClient         bool
	traceRTP          bool
	datachannel       bool
	dataChannelFlowID uint
	udpPort           uint
	rtcpSendPort      uint
	rtcpRecvPort      uint
	rtpFlowID         uint
	rtcpSendFlowID    uint
	rtcpRecvFlowID    uint
	udpRecvBufferSize int
	transport         string

	media media.Flags
}

func (r *Receive) Help() string {
	return "Run receiver pipeline"
}

func (r *Receive) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive", flag.ExitOnError)
	fs.StringVar(&r.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&r.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.UintVar(&r.roqMapping, "roq-mapping", 0, "RTP mapping to QUIC. 0: datagrams, 1: stream per packet, 2: single stream")
	fs.BoolVar(&r.roqServer, "roq-server", false, "Use RoQ server transport")
	fs.BoolVar(&r.roqClient, "roq-client", false, "Use roQ client transport")
	fs.BoolVar(&r.traceRTP, "trace-rtp-recv", false, "Log incoming RTP packets")
	fs.BoolVar(&r.datachannel, "dc", false, "Send/Receive data with data channels")
	fs.UintVar(&r.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")
	fs.UintVar(&r.udpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	fs.UintVar(&r.rtpFlowID, "rtp-flow-id", 0, "RTP Flow ID when using RTP over QUIC")
	fs.UintVar(&r.rtcpSendPort, "rtcp-send-port", 5002, "UDP port for outgoing RTCP stream")
	fs.UintVar(&r.rtcpRecvPort, "rtcp-recv-port", 5001, "UDP port for incoming RTCP stream")
	fs.UintVar(&r.rtcpSendFlowID, "rtcp-send-flow-id", 1, "RTCP Sender Flow ID when using RTP over QUIC")
	fs.UintVar(&r.rtcpRecvFlowID, "rtcp-recv-flow-id", 2, "RTCP Receiver Flow ID when using RTP over QUIC")

	fs.IntVar(&r.udpRecvBufferSize, "recv-buffer-size", r.udpRecvBufferSize, "Size of the UDP receive buffer in bytes, 0 leaves the operating system default")
	fs.StringVar(&r.transport, "transport", DefaultTransport,
		fmt.Sprintf("Transport for plain RTP, ignored when RoQ is enabled or when the media pipeline moves the packets itself (%v)", strings.Join(transportNames, ", ")))

	if err := r.media.ConfigureReceiver(fs); err != nil {
		return err
	}

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a receiver pipeline

Usage:
	%v receive [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", fs.Args())
		fs.Usage()
		os.Exit(1)
	}

	if r.roqMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2\n", r.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if (r.datachannel || r.roqMapping != 0) && (!r.roqServer && !r.roqClient) {
		fmt.Fprintf(os.Stderr, "Flag -%v, -%v and only valid for RoQ\n", "dc", "roq-mapping")
		fs.Usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, p := range []uint{
		r.rtcpRecvPort,
		r.rtcpSendPort,
		r.udpPort,
	} {
		if p > math.MaxUint16 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if r.roqClient && r.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	receiverConfig, err := r.media.ReceiverConfig("rtp-stream-sink")
	if err != nil {
		return err
	}

	pipeline, err := r.media.NewPipeline()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := pipeline.Close(); closeErr != nil {
			slog.Error("failed to close media pipeline", "error", closeErr)
		}
	}()

	if r.roqServer || r.roqClient {
		var closeRoQ func()
		closeRoQ, err = r.setupRoQ(ctx, pipeline, receiverConfig)
		if closeRoQ != nil {
			defer closeRoQ()
		}
	} else {
		err = r.setupPlainRTP(pipeline, receiverConfig)
	}
	if err != nil {
		return err
	}
	return pipeline.Run(ctx)
}

func (r *Receive) setupRoQ(ctx context.Context, pipeline media.Pipeline, config media.ReceiverConfig) (func(), error) {
	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(r.roqServer)),
		quictransport.SetLocalAddress(r.localAddr, r.udpPort),
		quictransport.SetRemoteAddress(r.remoteAddr, r.udpPort),
		quictransport.SetQLOGLabel("reicever"),
	}

	quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
	if err != nil {
		return nil, err
	}
	cleanup := func() { quicConn.Close() }

	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return cleanup, err
	}
	cleanup = func() {
		if closeErr := roqTransport.Close(); closeErr != nil {
			slog.Error("failed to close RoQ session", "error", closeErr)
		}
		quicConn.Close()
	}

	dcTransport, err := datachannels.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return cleanup, err
	}

	// set handlers for datagrams and streams
	// have to forward it ether to roq or dc
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(r.rtpFlowID) || flowID == uint64(r.rtcpRecvFlowID) || flowID == uint64(r.rtcpSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roq.NewQuicGoReceiveStream(rs))
			return
		}

		if r.datachannel {
			if readErr := dcTransport.ReadStream(ctx, datachannels.NewQuicGoReceiveStream(rs), flowID); readErr != nil {
				slog.Error("failed to read stream", "error", readErr)
			}
			return
		}

		slog.Error("unknown stream flow ID, closing stream", "flow-id", flowID)
		rs.CancelRead(0)
	}

	// start handler
	quicConn.StartHandlers()

	if r.datachannel {
		// setup data channel receiver
		// quic transports has to be started before
		dcReceiver, err := dcTransport.AddDataChannelReceiver(ctx, uint64(r.dataChannelFlowID))
		if err != nil {
			return cleanup, err
		}

		dataSink, err := data.NewSink(dcReceiver)
		if err != nil {
			return cleanup, err
		}

		go func() {
			if sinkErr := dataSink.Run(); sinkErr != nil {
				slog.Error("failed to run data sink", "error", sinkErr)
			}
		}()
	}

	rtpSrc, err := roqTransport.NewReceiveFlow(uint64(r.rtpFlowID), r.traceRTP)
	if err != nil {
		return cleanup, err
	}
	rtcpSink, err := roqTransport.NewSendFlow(uint64(r.rtcpSendFlowID), roq.SendMode(r.roqMapping), false)
	if err != nil {
		return cleanup, err
	}
	rtcpSrc, err := roqTransport.NewReceiveFlow(uint64(r.rtcpRecvFlowID), false)
	if err != nil {
		return cleanup, err
	}

	config.RTP = roqSource{ReadCloser: rtpSrc, conn: quicConn}
	config.RTCP = media.RTCPFlow{Send: rtcpSink, Recv: rtcpSrc}
	return cleanup, pipeline.AddReceiver(config)
}

// roqSource is a RoQ receive flow that also reports the round trip time of the
// QUIC connection it runs on, which the flow itself does not know. Nothing
// requires the interface, a pipeline only asks for it, so it is asserted here.
var _ mrtp.RTTSource = roqSource{}

type roqSource struct {
	io.ReadCloser
	conn *quictransport.Transport
}

// RTT implements mrtp.RTTSource.
func (s roqSource) RTT() time.Duration {
	return s.conn.GetRTT()
}

func (r *Receive) setupPlainRTP(pipeline media.Pipeline, config media.ReceiverConfig) error {
	switch r.transport {
	case transportGstUDP:
		// The pipeline receives the packets itself, from its own flags. Opening
		// a socket here would only bind the same port twice.
		return pipeline.AddReceiver(config)
	case transportUDP:
	default:
		return fmt.Errorf("unknown transport %q, available: %v", r.transport, transportNames)
	}

	rtpSrc, err := udp.Listen(address(r.localAddr, uint16(r.udpPort)), r.traceRTP,
		udp.ReceiveBufferSize(r.udpRecvBufferSize))
	if err != nil {
		return err
	}
	rtcpSink, err := udp.Dial(address(r.remoteAddr, uint16(r.rtcpSendPort)), false)
	if err != nil {
		return err
	}
	rtcpSrc, err := udp.Listen(address(r.localAddr, uint16(r.rtcpRecvPort)), false)
	if err != nil {
		return err
	}

	config.RTP = rtpSrc
	config.RTCP = media.RTCPFlow{Send: rtcpSink, Recv: rtcpSrc}
	return pipeline.AddReceiver(config)
}
