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
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	mrtpwebrtc "github.com/mengelbart/mrtp/webrtc"
	pionwebrtc "github.com/pion/webrtc/v4"
)

//go:embed client.html
var clientHTML []byte

type Controller struct {
	videoPath  string
	localAddr  string
	remoteAddr string
	localPort  string
	remotePort string
}

func NewController(videoPath, localAddr, localPort, remoteAddr, remotePort string) *Controller {
	return &Controller{
		videoPath:  videoPath,
		localAddr:  localAddr,
		localPort:  localPort,
		remoteAddr: remoteAddr,
		remotePort: remotePort,
	}
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
		msg := strings.Join(parts, " ")
		if strings.Contains(msg, "__STATUS__:") {
			slog.Info("Browser status", "status", strings.Replace(msg, "__STATUS__:", "", 1))
			return
		}
	})

	// singal server
	incomingSignaler := NewSignaler(
		func(sessionDesc *pionwebrtc.SessionDescription) error {
			fmt.Println("golang: got sessionDesc")
			payload, err := json.Marshal(sessionDesc)
			if err != nil {
				return err
			}
			js := fmt.Sprintf("window.applyRemoteSessionDescription(%s)", string(payload))
			return chromedp.Run(taskCtx, chromedp.Evaluate(js, nil))
		},
		func(candidate pionwebrtc.ICECandidateInit) error {
			fmt.Println("golang: got ICECandidateInit")
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

	// path := filepath.Join(dir, "DevToolsActivePort")
	// bs, err := os.ReadFile(path)
	// if err != nil {
	// 	return err
	// }
	// lines := bytes.Split(bs, []byte("\n"))
	// fmt.Printf("DevToolsActivePort has %d lines\n", len(lines))
	// for _, line := range lines {
	// 	fmt.Printf("%v\n", string(line))
	// }

	select {}
}

func writeHTML(remoteSignalURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := strings.ReplaceAll(string(clientHTML), "__REMOTE_SIGNAL_URL__", remoteSignalURL)
		_, _ = w.Write([]byte(html))
	})
}
