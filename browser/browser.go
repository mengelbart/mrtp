package browser

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/chromedp/chromedp"
)

type Controller struct{}

func NewController() *Controller {
	return &Controller{}
}

func (c *Controller) Run() error {
	dir, err := os.MkdirTemp("", "chromedp-example")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		chromedp.UserDataDir(dir),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	taskCtx, cancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancel()

	var buf []byte
	if err = chromedp.Run(
		taskCtx,
		chromedp.Navigate("https://google.com"),
		chromedp.FullScreenshot(&buf, 100),
	); err != nil {
		return err
	}

	if err = os.WriteFile("fullScreenshot.png", buf, 0o644); err != nil {
		return err
	}

	path := filepath.Join(dir, "DevToolsActivePort")
	bs, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := bytes.Split(bs, []byte("\n"))
	fmt.Printf("DevToolsActivePort has %d lines\n", len(lines))
	for _, line := range lines {
		fmt.Printf("%v\n", string(line))
	}

	return nil
}
