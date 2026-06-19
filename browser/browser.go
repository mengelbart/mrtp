package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	mrtpwebrtc "github.com/mengelbart/mrtp/webrtc"
	pionwebrtc "github.com/pion/webrtc/v4"
)

//go:embed client.html
var clientHTML []byte

type Option func(*Controller) error

type Controller struct {
	videoPath  string
	localAddr  string
	remoteAddr string
	localPort  string
	remotePort string

	datachannel  bool
	dcStartDelay uint
	dcSourceFile string
}

func UseDatachannel(startDelay uint, sourceFile string) Option {
	return func(c *Controller) error {
		c.datachannel = true
		c.dcStartDelay = startDelay
		c.dcSourceFile = sourceFile
		return nil
	}
}

func NewController(videoPath, localAddr, localPort, remoteAddr, remotePort string, opts ...Option) (*Controller, error) {
	c := &Controller{
		videoPath:  videoPath,
		localAddr:  localAddr,
		localPort:  localPort,
		remoteAddr: remoteAddr,
		remotePort: remotePort,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	return c, nil
}

func (c *Controller) Run() error {
	dir, err := os.MkdirTemp("", "chromedp-example")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()

	// open browser
	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.UserDataDir(dir),
		chromedp.Flag("use-fake-ui-for-media-stream", true),
		chromedp.Flag("use-fake-device-for-media-stream", true),
		chromedp.Flag("use-file-for-fake-video-capture", c.videoPath),
		chromedp.Flag("unsafely-treat-insecure-origin-as-secure", fmt.Sprintf("http://%s:9090", c.localAddr)),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	taskCtx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()

	// catch logs from browser
	chromedp.ListenTarget(taskCtx, func(ev any) {
		consoleEvent, ok := ev.(*runtime.EventConsoleAPICalled)
		if !ok {
			return
		}

		var parts []string
		for _, arg := range consoleEvent.Args {
			if arg.Value != nil {
				parts = append(parts, fmt.Sprint(arg.Value))
			}
		}
		msg := strings.Join(parts, "")

		if strings.Contains(msg, "__STATUS__:") {
			if unq, err := strconv.Unquote(msg); err == nil {
				msg = unq
			}

			slog.Info("Browser status", "status", strings.Replace(msg, "__STATUS__:", "", 1))

		} else if strings.Contains(msg, "__WEBRTC_STATS_ERROR__:") {
			slog.Info("Webrtc status", "status", strings.Replace(msg, "__WEBRTC_STATS_ERROR__:", "", 1))

		} else if strings.Contains(msg, "__WEBRTC_TR__:") {
			msg, err := strconv.Unquote(msg)
			if err != nil {
				panic(fmt.Sprintf("failed to parse target rate log: %v", err))
			}

			msg = strings.Replace(msg, "__WEBRTC_TR__:", "", 1)
			rate, err := strconv.Atoi(msg)
			if err != nil {
				panic(fmt.Sprintf("could not convert target rate to int: %v", err))
			}
			slog.Info("NEW_TARGET_MEDIA_RATE", "rate", rate)
		}
	})

	// singal server
	incomingSignaler := NewSignaler(
		func(sessionDesc *pionwebrtc.SessionDescription) error {
			payload, err := json.Marshal(sessionDesc)
			if err != nil {
				return err
			}
			js := fmt.Sprintf("window.applyRemoteSessionDescription(%s)", string(payload))
			return chromedp.Run(taskCtx, chromedp.Evaluate(js, nil))
		},
		func(candidate pionwebrtc.ICECandidateInit) error {
			payload, err := json.Marshal(candidate)
			if err != nil {
				return err
			}
			js := fmt.Sprintf("window.applyRemoteCandidate(%s)", string(payload))
			return chromedp.Run(taskCtx, chromedp.Evaluate(js, nil))
		},
	)
	signalingHandler := mrtpwebrtc.NewHTTPSignalingHandler(incomingSignaler)

	localSignalMux := http.NewServeMux()
	localSignalMux.HandleFunc("/candidate", signalingHandler.HandleCandidate)
	localSignalMux.HandleFunc("/session_description", signalingHandler.HandleSessionDescription)

	localSignalServer := &http.Server{
		Addr:    net.JoinHostPort(c.localAddr, c.localPort),
		Handler: localSignalMux,
	}
	defer func() {
		if err := localSignalServer.Close(); err != nil {
			panic(err)
		}
	}()

	go func() {
		if serveErr := localSignalServer.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("browser signaling server stopped: %v", serveErr)
		}
	}()

	// start web server to serve client HTML
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:9090", c.localAddr)) // TODO: verify port is not used by signaling server
	if err != nil {
		return err
	}

	remoteSignalURL := fmt.Sprintf("http://%v:%v", c.remoteAddr, c.remotePort)
	srv := &http.Server{Handler: writeHTML(remoteSignalURL)}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
		}
	}()

	// start browser
	if err := chromedp.Run(taskCtx,
		chromedp.Navigate(fmt.Sprintf("http://%s", ln.Addr().String())),
	); err != nil {
		return err
	}

	// start datachannel
	if c.datachannel {
		go func() {
			if err := c.StartDataChannel(taskCtx); err != nil {
				log.Printf("failed to start data channel: %v", err)
			}
		}()
	}

	select {}
}

func (c *Controller) StartDataChannel(taskCtx context.Context) error {
	if c.dcSourceFile == "" {
		// start random sender
		payload, err := json.Marshal(DataFile{Delay: int(c.dcStartDelay)})
		if err != nil {
			return err
		}
		js := fmt.Sprintf("window.startRandomDataSender(%s)", string(payload))
		err = chromedp.Run(taskCtx, chromedp.Evaluate(js, nil))
		if err != nil {
			return fmt.Errorf("failed to start random data sender: %v", err)
		}

		return nil
	}

	// start file sender
	payload, err := json.Marshal(DataFile{Delay: int(c.dcStartDelay)})
	if err != nil {
		return err
	}
	js := fmt.Sprintf("window.startFileDataSender(%s)", string(payload))
	err = chromedp.Run(taskCtx, chromedp.Evaluate(js, nil))
	if err != nil {
		return fmt.Errorf("failed to start random data sender: %v", err)
	}

	err = chromedp.Run(taskCtx,
		chromedp.SetUploadFiles(`#fileInput`, []string{c.dcSourceFile}),
	)
	if err != nil {
		return fmt.Errorf("failed to send file: %v", err)
	}

	return nil
}

func writeHTML(remoteSignalURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := strings.ReplaceAll(string(clientHTML), "__REMOTE_SIGNAL_URL__", remoteSignalURL)
		_, _ = w.Write([]byte(html))
	})
}

type DataFile struct {
	Delay int `json:"delay"`
}
