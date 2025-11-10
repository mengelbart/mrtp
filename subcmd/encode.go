package subcmd

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/y4m"
)

func init() {
	cmdmain.RegisterSubCmd("enc", func() cmdmain.SubCmd { return new(Encode) })
}

type Encode struct{}

func (e *Encode) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("enc", flag.ExitOnError)
	path := fs.String("file", "", "Input file")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run encoder

Usage:
	%s enc [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, h, err := y4m.NewReader(file)
	if err != nil {
		return err
	}

	if _, err = file.Seek(0, 0); err != nil {
		return err
	}

	encoder, err := mrtp.NewEncoder(mrtp.Config{
		Codec:       "vp8",
		Width:       uint(h.Width),
		Heigth:      uint(h.Height),
		TimebaseNum: h.FrameRate.Numerator,
		TimebaseDen: h.FrameRate.Denominator,
		TargetRate:  1_000_000,
	})
	if err != nil {
		return err
	}

	source, err := mrtp.NewY4MSource(file, encoder)
	if err != nil {
		return err
	}

	for {
		frame, err := source.GetFrame()
		if err != nil {
			return err
		}
		log.Printf("encoded frame: size=%v, pkt=%v", len(frame.Payload), frame.IsKeyFrame)
	}
}

func (e *Encode) Help() string {
	return "Run Encoder"
}
