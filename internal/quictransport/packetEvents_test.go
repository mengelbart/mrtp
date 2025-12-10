package quictransport

import (
	"testing"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/stretchr/testify/require"
)

func TestPacketEvent(t *testing.T) {

	acks := []nada.Acknowledgment{
		{
			SeqNr:   42,
			Arrival: time.UnixMilli(110),
			Marked:  true,
		},
		{
			SeqNr:   45,
			Arrival: time.UnixMilli(115),
			Marked:  true,
		},
	}

	// Create a buffered channel and populate it with the acknowledgments
	eventChan := make(chan nada.Acknowledgment, len(acks))
	for _, ack := range acks {
		eventChan <- ack
	}

	data, err := Marshal(eventChan, len(acks))
	require.NoError(t, err)

	for i := range acks {
		acks[i].Arrived = true // marshal sets all to arrived
	}

	res, err := UnmarshalFeedback(data)
	require.NoError(t, err)

	require.Equal(t, acks, res)
}
