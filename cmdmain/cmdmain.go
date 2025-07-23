// Package cmdmain implements commands and subcommands.
//
// The design idea is taken from [perkeep/cmdmain], but most of the code is
// modified. This package uses the [RegisterSubCmd] to allow users to add new
// subcommands. The implementation uses the same mechanism as perkeep. See
// [Perkeep LICENSE] for perkeeps copyright and license information.
//
// [perkeep/cmdmain]: https://github.com/perkeep/perkeep/tree/56726780f66b5654c1d7c01dc85b0e686ddbffd2/pkg/cmdmain
// [Perkeep LICENSE]: https://github.com/perkeep/perkeep/blob/56726780f66b5654c1d7c01dc85b0e686ddbffd2/COPYING
package cmdmain

import (
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"

	"github.com/mengelbart/mrtp/logging"
)

var (
	logFormat string
	logLevel  int
	logFile   string
)

type command struct {
	name   string
	help   string
	subCmd func(cmd string, args []string) error
}

type SubCmd interface {
	Help() string
	Exec(cmd string, args []string) error
}

var (
	subCmds = map[string]SubCmd{}
)

func RegisterSubCmd(name string, makeSubCmd func() SubCmd) {
	if _, ok := subCmds[name]; ok {
		log.Fatalf("duplicate subcommand: %q", name)
	}
	subCmds[name] = makeSubCmd()
}

func usage(name string) func() {
	return func() {
		fmt.Fprintf(os.Stderr, `%v is a tool to run media streaming pipelines

Usage:
	%v [flags] <command> [command flags]
`, name, name)

		fmt.Fprintln(os.Stderr, "\nCommands:")
		for name, cmd := range subCmds {
			fmt.Fprintf(os.Stderr, "  %-8s %s\n", name, cmd.Help())
		}

		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Run `mrtp <command> -h` to show full help for a command")
	}
}

func Main() {
	flag.StringVar(&logFile, "logfile", "", "Log file, empty string means stderr")
	flag.StringVar(&logFormat, "log-format", "text", "Logging format: text or json")
	flag.IntVar(&logLevel, "log-level", 0, "Logging level (slog.Level)")

	flag.Usage = usage(os.Args[0])
	flag.Parse()

	if len(flag.Args()) < 1 {
		fmt.Println("error: missing subcommand")
		flag.Usage()
		os.Exit(1)
	}

	var lf io.Writer = nil
	// use log file
	if logFile != "" {
		f, err := os.Create(logFile)
		if err != nil {
			fmt.Printf("failed to open log file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		lf = f
	}
	logging.Configure(logging.Format(logFormat), slog.Level(logLevel), lf)

	subCmd, ok := subCmds[flag.Arg(0)]
	if !ok {
		fmt.Println("error: unknown subcommand")
		flag.Usage()
		os.Exit(1)
	}

	subCmdArgs := flag.Args()[1:]
	if err := subCmd.Exec(os.Args[0], subCmdArgs); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
