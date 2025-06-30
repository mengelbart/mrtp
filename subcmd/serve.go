package subcmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/julienschmidt/httprouter"
	"github.com/mengelbart/mrtp/internal/http"
	"github.com/mengelbart/mrtp/internal/web"
)

type serveFlags struct {
	httpAddress  string
	httpsAddress string
	cert         string
	key          string
}

func Serve(cmd string, args []string) error {
	var sf serveFlags

	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	flags.StringVar(&sf.httpAddress, "http-address", "127.0.0.1:8080", "Server address")
	flags.StringVar(&sf.httpsAddress, "https-address", "127.0.0.1:4443", "Server address")
	flags.StringVar(&sf.cert, "cert", "localhost.pem", "TLS Certificate")
	flags.StringVar(&sf.key, "key", "localhost-key.pem", "TLS Certificate key")
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a frontend web server

Usage:
	%s serve [flags]

Flags:
`, cmd)
		flags.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	flags.Parse(args)

	if len(flags.Args()) > 1 {
		fmt.Printf("error: unknown extra arguments: %v\n", flag.Args()[1:])
		flags.Usage()
		os.Exit(1)
	}

	mux := httprouter.New()
	api := http.NewApi()
	api.RegisterRoutes(mux)

	_, err := web.NewHandler(web.Mux(mux))
	if err != nil {
		return err
	}

	s, err := http.NewServer(
		http.H1Address(sf.httpAddress),
		http.H2Address(sf.httpsAddress),
		http.H3Address(sf.httpsAddress),
		http.Handle(mux),
		http.CertificateFile(sf.cert),
		http.CertificateKeyFile(sf.key),
		http.RequestLogger(slog.Default()),
	)
	if err != nil {
		return err
	}

	return s.ListenAndServe()
}
