package subcmd

import (
	"flag"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/http"
	"github.com/mengelbart/mrtp/server"
)

func init() {
	cmdmain.RegisterSubCmd("serve", func() cmdmain.SubCmd { return new(Serve) })
}

type Serve struct {
	addr   string
	config server.Config
}

// Help implements cmdmain.SubCmd.
func (s *Serve) Help() string {
	return "Run signaling server"
}

func (s *Serve) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.StringVar(&s.addr, "addr", "127.0.0.1:8080", "HTTP signaling server address")
	fs.StringVar(&s.config.MediaHost, "media-host", "127.0.0.1", "IP to bind media sockets and restrict WebRTC ICE candidates to")
	fs.StringVar(&s.config.SourceDir, "source-dir", "", "Directory of VP8 and VP9 IVF files clients may request by name. Empty allows none.")
	fs.StringVar(&s.config.SinkDir, "sink-dir", "", "Directory to record received VP8 and VP9 tracks to as <session-id>.ivf. Empty drops them.")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a signaling server that accepts media sessions from clients

Usage:
	%s serve [flags]

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

	srv, err := server.New(s.config)
	if err != nil {
		return err
	}
	defer srv.Close()

	mux := nethttp.NewServeMux()
	srv.Register(mux)

	httpServer, err := http.NewServer(
		http.H1Address(s.addr),
		http.ListenH2(false),
		http.ListenH3(false),
		http.RedirectH1ToH3(false),
		http.Handle(mux),
		http.RequestLogger(slog.Default()),
	)
	if err != nil {
		return err
	}
	return httpServer.ListenAndServe()
}
