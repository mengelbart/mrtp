package subcmd

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mengelbart/moqtransport"
	"github.com/mengelbart/moqtransport/quicmoq"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/moq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("moq-pub", func() cmdmain.SubCmd { return new(MoQPub) })
}

type MoQPub struct{}

// Exec implements cmdmain.SubCmd.
func (m *MoQPub) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("moq-pub", flag.ExitOnError)
	flags.RegisterInto(fs, []flags.FlagName{
		flags.RemoteAddrFlag,
		flags.LocalAddrFlag,
	}...)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a MoQT publisher

Usage:
	%s moq-pub [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	w := moq.NewLocalTrack()

	go func() {
		ticker := time.NewTicker(time.Second)
		for now := range ticker.C {
			_, err := w.Write([]byte(now.Format(time.RFC3339)))
			if err != nil {
				panic(err)
			}
		}
	}()

	// TODO: Add flag to select server/client
	if true {
		handler := &handler{
			track: w,
		}
		l := quictransport.NewListener(handler)
		err := l.ListenAndHandle(flags.LocalAddr, &quic.Config{
			EnableDatagrams: true,
		}, []string{"moq-00"})
		if err != nil {
			return err
		}
	}

	return nil
}

// Help implements cmdmain.SubCmd.
func (m *MoQPub) Help() string {
	return "Run a MoQ publisher"
}

type handler struct {
	track *moq.LocalTrack
}

func (l *handler) Handle(conn *quic.Conn) {
	transport, err := moq.New(quicmoq.New(conn, moqtransport.PerspectiveServer))
	if err != nil {
		conn.CloseWithError(quic.ApplicationErrorCode(moqtransport.ErrorCodeInternal), "failed to setup session")
		return
	}
	err = transport.AddTrack([]string{"clock"}, "second", l.track)
	if err != nil {
		conn.CloseWithError(quic.ApplicationErrorCode(moqtransport.ErrorCodeInternal), "failed to setup session")
		return
	}
}
