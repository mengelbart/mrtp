package subcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mengelbart/mrtp/browser"
	"github.com/mengelbart/mrtp/cmdmain"
)

func init() {
	cmdmain.RegisterSubCmd("browser", func() cmdmain.SubCmd { return new(browserSubCmd) })
}

type browserSubCmd struct {
	localAddr      string
	remoteAddr     string
	sourceLocation string
	localPort      string
	remotePort     string
}

// Exec implements cmdmain.SubCmd.
func (b *browserSubCmd) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("browser", flag.ExitOnError)
	fs.StringVar(&b.sourceLocation, "source-location", "", "Location for filesource")

	fs.StringVar(&b.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&b.remoteAddr, "remote", "127.0.0.1", "Remote address")

	fs.StringVar(&b.localPort, "local-port", "8080", "Local port of HTTP signaling server to listen on")
	fs.StringVar(&b.remotePort, "remote-port", "8080", "Remote Port of HTTP signaling server to connect to")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a sender

Usage:
	%s browser [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(b.sourceLocation); os.IsNotExist(err) {
		fmt.Printf("Cannot find video file: %v\n", b.sourceLocation)
		fs.Usage()
		os.Exit(1)
	}

	var err error
	b.sourceLocation, err = filepath.Abs(b.sourceLocation)
	if err != nil {
		fmt.Printf("Error with video: %v\n", b.sourceLocation)
		fs.Usage()
		os.Exit(1)
	}

	ctrl := browser.NewController(b.sourceLocation, b.localAddr, b.localPort, b.remoteAddr, b.remotePort)
	return ctrl.Run()
}

// Help implements cmdmain.SubCmd.
func (b *browserSubCmd) Help() string {
	return "Run a remote controlled browser"
}
