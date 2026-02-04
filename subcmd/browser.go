package subcmd

import (
	"github.com/mengelbart/mrtp/browser"
	"github.com/mengelbart/mrtp/cmdmain"
)

func init() {
	cmdmain.RegisterSubCmd("browser", func() cmdmain.SubCmd { return new(browserSubCmd) })
}

type browserSubCmd struct{}

// Exec implements cmdmain.SubCmd.
func (b *browserSubCmd) Exec(cmd string, args []string) error {
	ctrl := browser.NewController()
	return ctrl.Run()
}

// Help implements cmdmain.SubCmd.
func (b *browserSubCmd) Help() string {
	return "Run a remote controlled browser"
}
