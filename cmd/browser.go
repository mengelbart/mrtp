package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mengelbart/mrtp/internal/browser"
)

func init() {
	registerSubCmd("browser", func() subCmd { return new(browserSubCmd) })
}

type browserSubCmd struct {
	localAddr      string
	remoteAddr     string
	sourceLocation string
	remotePort     string

	datachannel  bool
	dcStartDelay uint
	dcSourceFile string
}

// Exec implements subCmd.
func (b *browserSubCmd) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("browser", flag.ExitOnError)
	fs.StringVar(&b.sourceLocation, "source-location", "", "Location for filesource")
	fs.StringVar(&b.localAddr, "local", "127.0.0.1", "Local address to serve the browser page on")
	fs.StringVar(&b.remoteAddr, "remote", "127.0.0.1", "Remote address of the HTTP signaling server to connect to")
	fs.StringVar(&b.remotePort, "remote-port", "8080", "Remote port of the HTTP signaling server to connect to")
	fs.BoolVar(&b.datachannel, "dc", false, "Send/Receive data with data channels")
	fs.UintVar(&b.dcStartDelay, "dc-start-delay", 0, "Start delay in seconds before data channel source starts sending data.")
	fs.StringVar(&b.dcSourceFile, "dc-source", "", "File to be sent. If empty, random data will be sent.")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%s

Usage:
	%s browser [flags]

Flags:
`, b.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(b.sourceLocation); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Cannot find video file: %v\n", b.sourceLocation)
		fs.Usage()
		os.Exit(1)
	}

	var err error
	b.sourceLocation, err = filepath.Abs(b.sourceLocation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error with video: %v\n", b.sourceLocation)
		fs.Usage()
		os.Exit(1)
	}

	if b.datachannel && b.dcSourceFile != "" {
		if _, err := os.Stat(b.dcSourceFile); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Cannot find data channel source file: %v\n", b.dcSourceFile)
			fs.Usage()
			os.Exit(1)
		}

		b.dcSourceFile, err = filepath.Abs(b.dcSourceFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error with data channel source file: %v\n", b.dcSourceFile)
			fs.Usage()
			os.Exit(1)
		}
	}

	opts := []browser.Option{}
	if b.datachannel {
		opts = append(opts, browser.UseDatachannel(b.dcStartDelay, b.dcSourceFile))
	}

	ctrl, err := browser.NewController(b.sourceLocation, b.localAddr, b.remoteAddr, b.remotePort, opts...)
	if err != nil {
		return err
	}

	return ctrl.Run()
}

// Help implements subCmd.
func (b *browserSubCmd) Help() string {
	return "Run a remote controlled browser. Sends a video via WebRTC. Can connect to the webrtc subcmd"
}
