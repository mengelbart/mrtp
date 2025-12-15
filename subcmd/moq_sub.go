package subcmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/moq"
	"github.com/mengelbart/mrtp/quictransport"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("moq-sub", func() cmdmain.SubCmd { return new(MoQSub) })
}

type MoQSub struct{}

// Exec implements cmdmain.SubCmd.
func (m *MoQSub) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("moq-pub", flag.ExitOnError)
	flags.RegisterInto(fs, []flags.FlagName{
		flags.RemoteAddrFlag,
		flags.LocalAddrFlag,
	}...)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a MoQT subscriber

Usage:
	%s moq-sub [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	var transport *moq.Transport
	// TODO: Add flag to select server/client
	if true {
		c, err := quictransport.OpenClientConn(flags.RemoteAddr, &quic.Config{
			EnableDatagrams: true,
		}, []string{"moq-00"})
		if err != nil {
			return err
		}
		transport, err = moq.New(quicmoq.New(c, moqtransport.PerspectiveClient))
		if err != nil {
			return err
		}
	}

	rt, err := transport.Subscribe(context.Background(), []string{"clock"}, "second")
	if err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		n, err := rt.Read(buf)
		if err != nil {
			return err
		}
		fmt.Printf("new object: %v\n", string(buf[:n]))
	}
}

// Help implements cmdmain.SubCmd.
func (m *MoQSub) Help() string {
	return "Run a MoQ subscriber"
}
