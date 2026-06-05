package subcmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/internal/http"
)

func init() {
	cmdmain.RegisterSubCmd("serve", func() cmdmain.SubCmd { return new(Serve) })
}

type Serve struct {
	cert      string
	key       string
	httpAddr  string
	httpsAddr string
}

// Help implements cmdmain.SubCmd.
func (s *Serve) Help() string {
	return "Run web server"
}

func (s *Serve) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.StringVar(&s.cert, "cert", "localhost.pem", "TLS Certificate")
	fs.StringVar(&s.key, "key", "localhost-key.pem", "TLS Certificate Key")
	fs.StringVar(&s.httpAddr, "http-addr", "127.0.0.1:8080", "HTTP Server address")
	fs.StringVar(&s.httpsAddr, "https-addr", "127.0.0.1:4443", "HTTPS Server address")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a frontend web server

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

	if len(fs.Args()) > 1 {
		fmt.Printf("error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	// mux := httprouter.New()

	// _, err := web.NewHandler(web.Mux(mux))
	// if err != nil {
	// 	return err
	// }

	api, err := http.NewApi()
	if err != nil {
		return err
	}
	router := http.NewRouter(api)

	server, err := http.NewServer(
		http.H1Address(s.httpAddr),
		http.H2Address(s.httpsAddr),
		http.H3Address(s.httpsAddr),
		http.Handle(router),
		http.CertificateFile(s.cert),
		http.CertificateKeyFile(s.key),
		http.RequestLogger(slog.Default()),
	)
	if err != nil {
		return err
	}

	return server.ListenAndServe()
}
