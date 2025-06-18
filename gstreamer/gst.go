package gstreamer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-gst/go-glib/glib"
	"github.com/go-gst/go-gst/pkg/gst"
)

func Run(pipeline gst.Pipeline) error {
	mainloop := glib.NewMainLoop(glib.MainContextDefault(), false)

	for msg := range pipeline.GetBus().Messages(context.Background()) {
		var err error
		switch msg.Type() {
		case gst.MessageEos:
			err = errors.New("end-of-stream")
		case gst.MessageError:
			var debug string
			debug, err = msg.ParseError()
			fmt.Println("DEBUG:", debug)
		}

		if err != nil {
			slog.Error("pipeline message error", "error", err)
			// pipeline.BlockSetState(gst.StateNull, nil)
			pipeline.BlockSetState(gst.StateNull, gst.ClockTime(time.Second))

			mainloop.Quit()
			return err
		}
	}

	if ret := pipeline.SetState(gst.StatePlaying); ret != gst.StateChangeSuccess {
		return fmt.Errorf("failed to set pipeline state to playing: %v", ret)
	}

	mainloop.Run()
	return nil
}

func SetProperties(e gst.Element, pp map[string]any) error {
	//	for k, v := range pp {
	//		if err := e.SetObjectProperty(k, v); err != nil {
	//			return err
	//		}
	//	}
	return nil
}
