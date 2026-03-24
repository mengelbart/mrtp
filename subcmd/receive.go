package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("receive", func() cmdmain.SubCmd { return new(Receive) })
}

// UDPRecvBufferSize is the default UDP Receive Buffer size for the Gstreamer
// udpsrc element
var UDPRecvBufferSize int

type StreamSinkFactory interface {
	ConfigureFlags(*flag.FlagSet)
	MakeStreamSink(name string, payloadType int) (gstreamer.RTPSinkBin, error)
}

type gstreamerVideoStreamSinkFactory struct {
}

func (f *gstreamerVideoStreamSinkFactory) ConfigureFlags(fs *flag.FlagSet) {
	newFlags := []flags.FlagName{
		flags.SinkTypeFlag,
		flags.SinkLocationFlag,
		flags.LogQuicFlag,
	}

	// check if codec flag is already registered - relevant for webrtc subcmd
	if fs.Lookup(string(flags.CodecFlag)) == nil {
		newFlags = append(newFlags, flags.CodecFlag)
	}
	flags.RegisterInto(fs, newFlags...)
}

func (f *gstreamerVideoStreamSinkFactory) MakeStreamSink(name string, pt int) (gstreamer.RTPSinkBin, error) {
	codec, error := mrtp.NewCodec(flags.Codec)
	if error != nil {
		return nil, error
	}

	return gstreamer.NewStreamSink(
		name,
		gstreamer.StreamSinkCodec(codec),
		gstreamer.StreamSinkType(gstreamer.SinkType(flags.SinkType)),
		gstreamer.StreamSinkLocation(flags.SinkLocation),
		gstreamer.StreamSinkPayloadType(pt),
	)
}

var DefaultStreamSinkFactory StreamSinkFactory = &gstreamerVideoStreamSinkFactory{}

type Receive struct {
	localAddr  string
	remoteAddr string
	receiver   *gstreamer.RTPBin
	sink       gstreamer.RTPSinkBin
	roqMapping uint
	roqServer  bool
	roqClient  bool
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

	// swap default values
	flags.SwapRTCPDefaults()

	flags.RegisterInto(fs, []flags.FlagName{
		flags.RTPPortFlag,
		flags.RTCPSendPortFlag,
		flags.RTCPRecvPortFlag,
		flags.RTPFlowIDFlag,
		flags.RTCPRecvFlowIDFlag,
		flags.RTCPSendFlowIDFlag,
		flags.GstCCFBFlag,
		flags.TraceRTPRecvFlag,
		flags.NadaFeedbackFlag,
		flags.DataChannelFlag,
		flags.NadaFeedbackFlowIDFlag,
		flags.DataChannelFlowIDFlag,
	}...)

	fs.IntVar(&UDPRecvBufferSize, "recv-buffer-size", UDPRecvBufferSize, "UDP receive 'buffer-size' of Gstreamer udpsrc element")

	DefaultStreamSinkFactory.ConfigureFlags(fs)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a receiver pipeline

Usage:
	%v receive [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	if len(fs.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	if r.roqMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2\n", r.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if (flags.DataChannel || flags.LogQuic || flags.NadaFeedback || r.roqMapping != 0) && !(r.roqServer || r.roqClient) {
		fmt.Fprintf(os.Stderr, "Flag -%v, -%v, -%v and -%v only valid for RoQ\n", flags.DataChannelFlag, flags.LogQuicFlag, flags.NadaFeedbackFlag, "roq-mapping")
		fs.Usage()
		os.Exit(1)
	}

	if flags.SinkType == uint(gstreamer.Filesink) && len(flags.SinkLocation) == 0 {
		return errors.New("file-sink requires a location to be set via the -sink-location flag")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, p := range []uint{
		flags.RTCPRecvPort,
		flags.RTCPSendPort,
		flags.RTPPort,
	} {
		if p > math.MaxUint32 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if r.roqClient && r.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	var err error
	r.receiver, err = gstreamer.NewRTPBin()
	if err != nil {
		return err
	}

	r.sink, err = DefaultStreamSinkFactory.MakeStreamSink("rtp-stream-sink", 96)
	if err != nil {
		return err
	}

	if r.roqServer || r.roqClient {
		err = r.setupRoQ(ctx)
	} else {
		err = r.setupUDP()
	}
	if err != nil {
		return err
	}
	return r.receiver.Run()
}

func (r *Receive) setupRoQ(ctx context.Context) error {
	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(r.roqServer)),
		quictransport.SetLocalAddress(r.localAddr, flags.RTPPort), // TODO: which port to use?
		quictransport.SetRemoteAddress(r.remoteAddr, flags.RTPPort),
	}

	if flags.NadaFeedback {
		feedbackDelta := time.Duration(20 * time.Millisecond)
		quicOptions = append(quicOptions, quictransport.EnableNADAfeedback(feedbackDelta, uint64(flags.NadaFeedbackFlowID)))
	}

	if flags.LogQuic {
		quicOptions = append(quicOptions, quictransport.EnableQLogs("./receiver.qlog"))
	}

	quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	dcTransport := quicConn.GetQuicDataChannel()

	// set handlers for datagrams and streams
	// have to forward it ether to roq or dc
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(flags.RTPFlowID) || flowID == uint64(flags.RTCPRecvFlowID) || flowID == uint64(flags.RTCPSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		if flags.DataChannel {
			dcTransport.ReadStream(context.Background(), rs, flowID)
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}

	// start handler
	quicConn.StartHandlers()

	if flags.DataChannel {
		// setup data channel receiver
		// quic tranpsorts has to be started before
		dcReceiver, err := dcTransport.AddDataChannelReceiver(uint64(flags.DataChannelFlowID))
		if err != nil {
			return err
		}

		dataSink, err := data.NewSink(dcReceiver)
		if err != nil {
			return err
		}

		go dataSink.Run()
	}

	rtpSrc, err := roqTransport.NewReceiveFlow(uint64(flags.RTPFlowID), flags.TraceRTPRecv)
	if err != nil {
		return err
	}
	if err = r.receiver.AddRTPSink(0, r.sink); err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTPStreamFrom(0, rtpSrc, flags.GstCCFB); err != nil {
		return err
	}

	rtcpSink, err := roqTransport.NewSendFlow(uint64(flags.RTCPSendFlowID), roq.SendMode(r.roqMapping), false)
	if err != nil {
		return err
	}
	if err = r.receiver.SendRTCPForStream(0, rtcpSink); err != nil {
		return err
	}

	rtcpSrc, err := roqTransport.NewReceiveFlow(uint64(flags.RTCPRecvFlowID), false)
	if err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTCPFrom(rtcpSrc); err != nil {
		return err
	}
	return nil
}

func (r *Receive) setupUDP() error {
	rtpSrc, err := gstreamer.NewUDPSrc(
		r.localAddr,
		uint32(flags.RTPPort),
		gstreamer.EnabelUDPSrcPadProbe(flags.TraceRTPRecv),
		gstreamer.SetReceiveBufferSize(UDPRecvBufferSize),
	)
	if err != nil {
		return err
	}
	if err = r.receiver.AddRTPSink(0, r.sink); err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTPStreamFromGst(0, rtpSrc.GetGstElement(), flags.GstCCFB); err != nil {
		return err
	}

	rtcpSink, err := gstreamer.NewUDPSink(r.remoteAddr, uint32(flags.RTCPSendPort))
	if err != nil {
		return err
	}
	if err = r.receiver.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
		return err
	}

	rtcpSrc, err := gstreamer.NewUDPSrc(r.localAddr, uint32(flags.RTCPRecvPort))
	if err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
		return err
	}
	return nil
}
