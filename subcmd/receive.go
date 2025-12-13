package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"

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
	MakeStreamSink(name string, payloadType int, videoFilename string, flowID uint) (gstreamer.RTPSinkBin, error)
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

func (f *gstreamerVideoStreamSinkFactory) MakeStreamSink(name string, pt int, videoFilename string, flowID uint) (gstreamer.RTPSinkBin, error) {
	codec, error := mrtp.NewCodec(flags.Codec)
	if error != nil {
		return nil, error
	}

	return gstreamer.NewStreamSink(
		name,
		gstreamer.StreamSinkCodec(codec),
		gstreamer.StreamSinkType(gstreamer.SinkType(flags.SinkType)),
		gstreamer.StreamSinkLocation(videoFilename),
		gstreamer.StreamSinkPayloadType(pt),
		gstreamer.StreamSinkFlowID(flowID),
	)
}

var DefaultStreamSinkFactory StreamSinkFactory = &gstreamerVideoStreamSinkFactory{}

type Receive struct {
}

func (r *Receive) Help() string {
	return "Run receiver pipeline"
}

func (r *Receive) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive", flag.ExitOnError)

	// swap default values
	flags.SwapRTCPDefaults()

	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.RTPPortFlag,
		flags.RTCPSendPortFlag,
		flags.RTCPRecvPortFlag,
		flags.RTPFlowIDFlag,
		flags.RTCPRecvFlowIDFlag,
		flags.RTCPSendFlowIDFlag,
		flags.RoQServerFlag,
		flags.RoQClientFlag,
		flags.RoQMappingFlag,
		flags.GstCCFBFlag,
		flags.TraceRTPRecvFlag,
		flags.NadaFeedbackFlag,
		flags.DataChannelFlag,
		flags.NadaFeedbackFlowIDFlag,
		flags.DataChannelFlowIDFlag,
		flags.RTPFlowsFlag,
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

	if flags.RoQMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2\n", flags.RoQMappingFlag)
		fs.Usage()
		os.Exit(1)
	}

	if (flags.DataChannel || flags.LogQuic || flags.NadaFeedback || flags.RoQMapping != 0) && !(flags.RoQServer || flags.RoQClient) {
		fmt.Fprintf(os.Stderr, "Flag -%v, -%v, -%v and -%v only valid for RoQ\n", flags.DataChannelFlag, flags.LogQuicFlag, flags.NadaFeedbackFlag, flags.RoQMappingFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.SinkType == uint(gstreamer.Filesink) && len(flags.SinkLocation) == 0 {
		return errors.New("file-sink requires a location to be set via the -sink-location flag")
	}

	for _, p := range []uint{
		flags.RTCPRecvPort,
		flags.RTCPSendPort,
		flags.RTPPort,
	} {
		if p > math.MaxUint32 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if flags.RoQClient && flags.RoQServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	if flags.RoQServer || flags.RoQClient {
		return r.setupRoQ()
	} else {
		return r.setupUDP()
	}
}

type receivePipe struct {
	receiver *gstreamer.RTPBin
	sink     gstreamer.RTPSinkBin
}

func newRecieverPipe(videoFilename string, flowID uint) (*receivePipe, error) {
	rp := &receivePipe{}
	var err error
	rp.receiver, err = gstreamer.NewRTPBin()
	if err != nil {
		return nil, err
	}

	rp.sink, err = DefaultStreamSinkFactory.MakeStreamSink("rtp-stream-sink", 96, videoFilename, flowID)
	if err != nil {
		return nil, err
	}

	return rp, nil
}

func (r *Receive) setupRoQ() error {
	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(flags.RoQServer)),
		quictransport.SetLocalAdress(flags.LocalAddr, flags.RTPPort), // TODO: which port to use?
		quictransport.SetRemoteAdress(flags.RemoteAddr, flags.RTPPort),
	}

	if flags.NadaFeedback {
		feedbackDelta := uint64(20)
		quicOptions = append(quicOptions, quictransport.EnableNADAfeedback(feedbackDelta, uint64(flags.NadaFeedbackFlowID)))
	}

	if flags.LogQuic {
		quicOptions = append(quicOptions, quictransport.EnableQLogs("./receiver.qlog"))
	}

	quicConn, err := quictransport.New([]string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	roqTransport, err := roq.New(quicConn.GetQuicConnection())
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
		slog.Info("new uni stream", "streamID", rs.StreamID(), "flowID", flowID)

		if flowID%10 == uint64(flags.RTPFlowID) || flowID%10 == uint64(flags.RTCPRecvFlowID) || flowID%10 == uint64(flags.RTCPSendFlowID) {
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

	// create send pipes
	pipes := make([]*receivePipe, flags.RTPFlows)
	for i := range flags.RTPFlows {
		RTPFlowID := flags.RTPFlowID + uint(i*10)
		filename := "out.y4m"
		if i > 0 {
			filename = fmt.Sprintf("out%v.y4m", i)
		}
		pipe, err := newRecieverPipe(filename, RTPFlowID)
		if err != nil {
			return err
		}
		pipes[i] = pipe
	}

	for i := range flags.RTPFlows {
		RTCPSendFlowID := flags.RTCPSendFlowID + uint(i*10)
		RTCPRecvFlowID := flags.RTCPRecvFlowID + uint(i*10)
		RTPFlowID := flags.RTPFlowID + uint(i*10)

		rtpSrc, err := roqTransport.NewReceiveFlow(uint64(RTPFlowID), flags.TraceRTPRecv)
		if err != nil {
			return err
		}
		if err = pipes[i].receiver.AddRTPSink(0, pipes[i].sink); err != nil {
			return err
		}
		if err = pipes[i].receiver.ReceiveRTPStreamFrom(0, rtpSrc, flags.GstCCFB); err != nil {
			return err
		}

		rtcpSink, err := roqTransport.NewSendFlow(uint64(RTCPSendFlowID), roq.SendMode(flags.RoQMapping), false)
		if err != nil {
			return err
		}
		if err = pipes[i].receiver.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := roqTransport.NewReceiveFlow(uint64(RTCPRecvFlowID), false)
		if err != nil {
			return err
		}
		if err = pipes[i].receiver.ReceiveRTCPFrom(rtcpSrc); err != nil {
			return err
		}

	}

	// start all pipes
	for i := range flags.RTPFlows {
		go pipes[i].receiver.Run()
	}

	select {}
}

func (r *Receive) setupUDP() error {
	pipe, err := newRecieverPipe("out.y4m", 0)
	if err != nil {
		return err
	}

	rtpSrc, err := gstreamer.NewUDPSrc(
		flags.LocalAddr,
		uint32(flags.RTPPort),
		gstreamer.EnabelUDPSrcPadProbe(flags.TraceRTPRecv),
		gstreamer.SetReceiveBufferSize(UDPRecvBufferSize),
	)
	if err != nil {
		return err
	}
	if err = pipe.receiver.AddRTPSink(0, pipe.sink); err != nil {
		return err
	}
	if err = pipe.receiver.ReceiveRTPStreamFromGst(0, rtpSrc.GetGstElement(), flags.GstCCFB); err != nil {
		return err
	}

	rtcpSink, err := gstreamer.NewUDPSink(flags.RemoteAddr, uint32(flags.RTCPSendPort))
	if err != nil {
		return err
	}
	if err = pipe.receiver.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
		return err
	}

	rtcpSrc, err := gstreamer.NewUDPSrc(flags.LocalAddr, uint32(flags.RTCPRecvPort))
	if err != nil {
		return err
	}
	if err = pipe.receiver.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
		return err
	}

	return pipe.receiver.Run()
}
