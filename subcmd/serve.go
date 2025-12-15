package subcmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/julienschmidt/httprouter"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/http"
	"github.com/mengelbart/mrtp/internal/web"
)

func init() {
	cmdmain.RegisterSubCmd("serve", func() cmdmain.SubCmd { return new(Serve) })
}

type Serve struct{}

// Help implements cmdmain.SubCmd.
func (s *Serve) Help() string {
	return "Run web server"
}

func (s *Serve) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	flags.RegisterInto(fs, []flags.FlagName{
		flags.HTTPAddrFlag,
		flags.HTTPSAddrFlag,
		flags.CertFlag,
		flags.KeyFlag,
	}...)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a frontend web server

Usage:
	%s serve [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	if len(fs.Args()) > 1 {
		fmt.Printf("error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	mux := httprouter.New()
	api := http.NewApi()
	api.RegisterRoutes(mux)

	_, err := web.NewHandler(web.Mux(mux))
	if err != nil {
		return err
	}

	server, err := http.NewServer(
		http.H1Address(flags.HTTPAddr),
		http.H2Address(flags.HTTPSAddr),
		http.H3Address(flags.HTTPSAddr),
		http.Handle(mux),
		http.CertificateFile(flags.Cert),
		http.CertificateKeyFile(flags.Key),
		http.RequestLogger(slog.Default()),
	)
	if err != nil {
		return err
	}

	return server.ListenAndServe()
}
