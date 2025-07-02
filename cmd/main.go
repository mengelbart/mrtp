package main

import (
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/mengelbart/mrtp/subcmd"
)

type rootFlags struct {
	verbose bool
}

type command struct {
	name string
	help string
	run  func(cmd string, args []string) error
}

var commands = []command{
	{name: "send", help: "Run sender pipeline", run: subcmd.Send},
	{name: "receive", help: "Run receiver pipeline", run: subcmd.Receive},
	{name: "webrtc", help: "Run webrtc peer", run: subcmd.WebRTC},
	{name: "serve", help: "Run web server", run: subcmd.Serve},
	{name: "help", help: "Print help", run: help},
}

func main() {
	var rf rootFlags

	flag.BoolVar(&rf.verbose, "verbose", false, "enable verbose logging")

	flag.Usage = usage
	flag.Parse()

	if len(flag.Args()) < 1 {
		fmt.Println("error: missing subcommand")
		flag.Usage()
		os.Exit(1)
	}

	subCmd := flag.Arg(0)
	subCmdArgs := flag.Args()[1:]

	cmdIdx := slices.IndexFunc(commands, func(cmd command) bool {
		return cmd.name == subCmd
	})

	if cmdIdx < 0 {
		fmt.Println("error: unknown subcommand")
		flag.Usage()
		os.Exit(1)
	}

	if err := commands[cmdIdx].run(os.Args[0], subCmdArgs); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func help(cmd string, args []string) error {
	flag.Usage()
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `mrtp is a tool to run media streaming pipelines

Usage:
	mrtp [flags] <command> [command flags]`)

	fmt.Fprintln(os.Stderr, "\nCommands:")
	for _, cmd := range commands {
		fmt.Fprintf(os.Stderr, "  %-8s %s\n", cmd.name, cmd.help)
	}

	fmt.Fprintln(os.Stderr, "\nFlags:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Run `mrtp <command> -h` to show full help for a command")
}
