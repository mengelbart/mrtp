package subcmd

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/ivf"
	"github.com/mengelbart/mrtp/roq"
	"github.com/mengelbart/mrtp/rtp"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("roq-send-media", func() cmdmain.SubCmd { return new(roqSendMedia) })
}

type roqSendMedia struct {
	role           bool // true = server , false = client
	localAddr      string
	localPort      uint
	remoteAddr     string
	remotePort     uint
	quicCC         uint
	quicPacer      uint
	gcc            bool
	nada           bool
	qlog           bool
	maxTargetRate  int
	feedbackFlowID uint64
	location       string
}

// Exec implements cmdmain.SubCmd.
func (r *roqSendMedia) Exec(cmd string, args []string) error {
	r.setupFlags(cmd, args)

	options := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(r.role)),
		quictransport.SetQuicCC(int(r.quicCC)),
		quictransport.SetLocalAdress(r.localAddr, r.localPort),
		quictransport.SetRemoteAdress(r.remoteAddr, r.remotePort),
		quictransport.WithPacer(int(r.quicPacer)),
	}

	if r.gcc {
		options = append(
			options,
			quictransport.EnableGCC(
				1_000_000,
				150_000,
				r.maxTargetRate,
				r.feedbackFlowID,
			),
		)
	}
	if r.nada {
		options = append(
			options,
			quictransport.EnableGCC(
				1_000_000,
				150_000,
				r.maxTargetRate,
				r.feedbackFlowID,
			),
		)
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

	file, err := os.Open(r.location)
	if err != nil {
		return err
	}
	defer file.Close()

	src, err := ivf.NewSource(file)
	if err != nil {
		return err
	}
	defer src.Close()

	ps := mrtp.NewPeriodicSource(src, 33*time.Millisecond)

	sf, err := transport.NewSendFlow(0, false)
	if err != nil {
		return err
	}

	f := &mrtp.Flow{
		Input:         ps,
		Output:        sf,
		Containerizer: rtp.NewPacketizer(),
	}

	e := mrtp.NewEndpoint()
	e.AddFlow(f)

	return e.Run()
}

// Help implements cmdmain.SubCmd.
func (r *roqSendMedia) Help() string {
	return "Run a RTP over QUIC (RoQ) Media Sender"
}

func (r *roqSendMedia) setupFlags(cmd string, args []string) {
	fs := flag.NewFlagSet("roq-send-media", flag.ExitOnError)

	flags.RegisterInto(fs, []flags.FlagName{
		flags.QuicCCFlag,
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.QuicPacerFlag,
		flags.CCgccFlag,
		flags.CCnadaFlag,
		flags.MaxTragetRateFlag,
		flags.NadaFeedbackFlowIDFlag,
	}...)

	fs.BoolVar(&r.role, "server", false, "Run QUIC in server mode")
	fs.UintVar(&r.localPort, "local-port", 0, "Local port")
	fs.UintVar(&r.remotePort, "remote-port", 0, "Remote port")
	fs.StringVar(&r.location, "location", "input.ivf", "Input file location")
	fs.BoolVar(&r.qlog, "qlog", false, "Enable QLOG")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a RTP over QUIC (RoQ) Media Sender

Usage:
	%s roq-send-media [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	r.localAddr = flags.LocalAddr
	r.remoteAddr = flags.RemoteAddr
	r.quicCC = flags.QuicCC
	r.quicPacer = flags.QuicPacer
	r.gcc = flags.CCgcc
	r.nada = flags.CCnada
	r.maxTargetRate = int(flags.MaxTargetRate)
	r.feedbackFlowID = uint64(flags.NadaFeedbackFlowID)
}
