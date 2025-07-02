package gstreamer

import (
	"fmt"

	"github.com/go-gst/go-glib/glib"
	"github.com/go-gst/go-gst/gst"
)

func runPipeline(pipeline *gst.Pipeline) error {
	mainloop := glib.NewMainLoop(glib.MainContextDefault(), false)

	pipeline.GetPipelineBus().AddWatch(func(msg *gst.Message) bool {
		switch msg.Type() {
		case gst.MessageEOS:
			pipeline.BlockSetState(gst.StateNull)
			mainloop.Quit()
		case gst.MessageError:
			err := msg.ParseError()
			fmt.Println("ERROR:", err.Error())
			if debug := err.DebugString(); debug != "" {
				fmt.Println("DEBUG:", debug)
			}
			mainloop.Quit()
		}
		return true
	})

	if err := pipeline.SetState(gst.StatePlaying); err != nil {
		return err
	}

	mainloop.Run()
	return nil
}

func SetProperties(e *gst.Element, pp map[string]any) error {
	for k, v := range pp {
		if err := e.SetProperty(k, v); err != nil {
			return err
		}
	}
	return nil
}
