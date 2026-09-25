package main

import (
	"flag"
)

func init() {
	registerSubCmd("help", func() subCmd { return new(help) })
}

type help struct{}

// Exec implements subCmd.
func (h *help) Exec(cmd string, args []string) error {
	flag.Usage()
	return nil
}

// Help implements subCmd.
func (h *help) Help() string {
	return "Print help"
}
