//go:build cgo

package subcmd

// Media pipeline implementations register themselves, so they only have to be
// linked in. They need cgo, the commands that use them do not.
import (
	_ "github.com/mengelbart/mrtp/gopipe"
	_ "github.com/mengelbart/mrtp/gstreamer"
)
