package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	modified := false
	version := &versionSubCmd{
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
	registerSubCmd("version", func() subCmd { return version })
}

type versionSubCmd struct {
	path      string
	version   string
	gitCommit string
	gitDate   string
	goVersion string
}

// Exec implements subCmd.
func (v *versionSubCmd) Exec(cmd string, args []string) error {
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, err := fmt.Printf(`%s
	Version:	%s
	Git commit:	%s
	Built:		%s
	Go Version:	%s
`, v.path, v.version, v.gitCommit, v.gitDate, v.goVersion)
	return err
}

// Help implements subCmd.
func (v *versionSubCmd) Help() string {
	return "version prints out version information"
}
