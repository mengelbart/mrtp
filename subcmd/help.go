package subcmd

import (
	"flag"

	"github.com/mengelbart/mrtp/cmdmain"
)

func init() {
	cmdmain.RegisterSubCmd("help", func() cmdmain.SubCmd { return new(help) })
}

type help struct{}

// Exec implements cmdmain.SubCmd.
func (h *help) Exec(cmd string, args []string) error {
	flag.Usage()
	return nil
}

// Help implements cmdmain.SubCmd.
func (h *help) Help() string {
	return "Print help"
}
