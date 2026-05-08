package subcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/ivf"
	"github.com/mengelbart/mrtp/roq"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("roq-receive-media", func() cmdmain.SubCmd { return new(roqReceiveMedia) })
}

type roqReceiveMedia struct {
	role           bool
	localAddr      string
	localPort      uint
	remoteAddr     string
	remotePort     uint
	qlog           bool
	feedbackFlowID uint64
	location       string
}

// Exec implements cmdmain.SubCmd.
func (r *roqReceiveMedia) Exec(cmd string, args []string) error {
	r.setupFlags(cmd, args)

	options := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(r.role)),
		quictransport.SetLocalAdress(r.localAddr, r.localPort),
		quictransport.SetRemoteAdress(r.remoteAddr, r.remotePort),
	}
	if r.qlog {
		options = append(options, quictransport.EnableQLogs("./sender.qlog"))
	}
	quicConn, err := quictransport.New([]string{roqALPN}, options...)
	if err != nil {
		return err
	}
	transport, err := roq.New(quicConn.GetQuicConnection())
	if err != nil {
		return err
	}
	quicConn.HandleDatagram = func(flowID uint64, datagram []byte) {
		transport.HandleDatagram(datagram)
	}
	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		transport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
	}
	quicConn.StartHandlers()

	file, err := os.Create(r.location)
	if err != nil {
		return err
	}
	defer file.Close()

	sink, err := ivf.NewSink(file)
	if err != nil {
		return err
	}
	defer sink.Close()

	rf, err := transport.NewReceiveFlow(0, true)
	if err != nil {
		return err
	}

	f := &mrtp.Flow{
		Input:  rf,
		Output: sink,
	}
	e := mrtp.NewEndpoint()
	e.AddFlow(f)

	return e.Run()
}

// Help implements cmdmain.SubCmd.
func (r *roqReceiveMedia) Help() string {
	return "Run a RTP over QUIC (RoQ) Media Receiver"
}

func (r *roqReceiveMedia) setupFlags(cmd string, args []string) {
	fs := flag.NewFlagSet("roq-receive-media", flag.ExitOnError)

	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.NadaFeedbackFlowIDFlag,
	}...)

	fs.BoolVar(&r.role, "server", false, "Run QUIC in server mode")
	fs.UintVar(&r.localPort, "local-port", 0, "Local port")
	fs.UintVar(&r.remotePort, "remote-port", 0, "Remote port")
	fs.StringVar(&r.location, "location", "output.ivf", "Output file location")
	fs.BoolVar(&r.qlog, "qlog", false, "Enable QLOG")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a RTP over QUIC (RoQ) Media Receiver

Usage:
	%s roq-receive-media [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	r.localAddr = flags.LocalAddr
	r.remoteAddr = flags.RemoteAddr
	r.feedbackFlowID = uint64(flags.NadaFeedbackFlowID)
}
