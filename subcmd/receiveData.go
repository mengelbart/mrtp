package subcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	datasrc "github.com/mengelbart/mrtp/data-src"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/flags"
	quicutils "github.com/mengelbart/mrtp/quic-utils"
)

func init() {
	cmdmain.RegisterSubCmd("receive-data", func() cmdmain.SubCmd { return new(ReceiveData) })
}

// ReceiveData is a command to run a receiver pipeline for data channels.
type ReceiveData struct {
}

func (r *ReceiveData) Help() string {
	return "Run receiver pipeline for data channels"
}

func (r *ReceiveData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive-data", flag.ExitOnError)

	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
	}...)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%v receive-data [flags]

Flags:
`, r.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	roqOptions := []datachannels.Option{
		datachannels.WithRole(quicutils.Role(quicutils.RoleServer)),
		datachannels.SetQuicCC(int(flags.QuicCC)),
		datachannels.SetLocalAdress(flags.LocalAddr, 8080),
		datachannels.SetRemoteAdress(flags.RemoteAddr, 8080),
	}

	transport, err := datachannels.New(roqOptions...)
	if err != nil {
		return err
	}

	receiver, err := transport.AddDataChannelReceiver(42)
	if err != nil {
		return err
	}

	sink, err := datasrc.NewSink()
	if err != nil {
		panic(err)
	}

	sink.AddDataTransportSink(receiver)

	return sink.Run()
}
