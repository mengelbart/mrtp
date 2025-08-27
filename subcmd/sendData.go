package subcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	datasrc "github.com/mengelbart/mrtp/data-src"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/flags"
	quicutils "github.com/mengelbart/mrtp/quic-utils"
)

var (
	sourceFile string
	rateLimit  int
)

func init() {
	cmdmain.RegisterSubCmd("send-data", func() cmdmain.SubCmd { return new(SendData) })
}

// SendData is a command to run a receiver pipeline for data channels.
type SendData struct{}

func (s *SendData) Help() string {
	return "Run sender pipeline for data channels"
}

func (s *SendData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send-data", flag.ExitOnError)
	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.QuicCCFlag,
	}...)

	fs.StringVar(&sourceFile, "source-file", "", "File to be sent. If empty, random data will be sent.")
	fs.IntVar(&rateLimit, "rate-limit", 0, "Rate limit in bits per second. 0 means no limit.")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%s send [flags]

Flags:
`, s.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	if rateLimit < 0 {
		return errors.New("rate limit must be >= 0")
	}

	roqOptions := []datachannels.Option{
		datachannels.WithRole(quicutils.Role(quicutils.RoleClient)),
		datachannels.SetQuicCC(int(flags.QuicCC)),
		datachannels.SetLocalAdress(flags.LocalAddr, 8080),
		datachannels.SetRemoteAdress(flags.RemoteAddr, 8080),
	}

	transport, err := datachannels.New(roqOptions...)
	if err != nil {
		return err
	}

	sender, err := transport.NewDataChannelSender(42, 0)
	if err != nil {
		return err
	}

	sourceOptions := []datasrc.DataBinOption{}
	if sourceFile != "" {
		// check if file exists
		if _, err := os.Stat(sourceFile); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("file does not exist: %v", sourceFile)
		}
		sourceOptions = append(sourceOptions, datasrc.DataBinUseFileSource(sourceFile))
	}

	if rateLimit > 0 {
		sourceOptions = append(sourceOptions, datasrc.DataBinUseRateLimiter(uint(rateLimit), 10000))
	}

	source, err := datasrc.NewDataBin(sourceOptions...)
	if err != nil {
		return err
	}

	source.AddDataTransportSink(sender)

	return source.Run()
}
