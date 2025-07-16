package subcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/internal/quic"
)

type relayFlags struct {
}

func Relay(cmd string, args []string) error {
	flags := flag.NewFlagSet("relay", flag.ExitOnError)

	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a MoQ Relay

Usage:
	%v relay [flags]

Flags:
`, cmd)
		flags.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}

	server, err := quic.NewServer()
	if err != nil {
		return err
	}
	return server.Listen()
}
