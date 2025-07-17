package subcmd

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/mengelbart/mrtp/cmdmain"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	modified := false
	version := &Version{
		path:      info.Main.Path,
		goVersion: "",
		version:   info.Main.Version,
		gitCommit: "",
		gitDate:   "",
	}
	version.goVersion = runtime.Version()
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			version.gitCommit = setting.Value
		case "vcs.time":
			version.gitDate = setting.Value
		case "vcs.modified":
			modified = true
		}
	}
	if modified {
		version.gitCommit += "+dirty"
	}
	cmdmain.RegisterSubCmd("version", func() cmdmain.SubCmd { return version })
}

type Version struct {
	path      string
	version   string
	gitCommit string
	gitDate   string
	goVersion string
}

// Exec implements cmdmain.SubCmd.
func (v *Version) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Print version information

Usage:
	%s version [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	fmt.Fprintf(os.Stdout, `%s
	Version:	%s
	Git commit:	%s
	Built:		%s
	Go Version:	%s
`, v.path, v.version, v.gitCommit, v.gitDate, v.goVersion)
	return nil
}

// Help implements cmdmain.SubCmd.
func (v *Version) Help() string {
	return "version prints out version information"
}
