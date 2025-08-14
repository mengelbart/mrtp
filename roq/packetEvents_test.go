package roq

import (
	"testing"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/stretchr/testify/require"
)

func TestPacketEvent(t *testing.T) {

	acks := PacketEvents{
		PacketEvents: []nada.Acknowledgment{
			{
				SeqNr:     42,
				Departure: time.UnixMilli(100),
				Arrival:   time.UnixMilli(110),
				SizeBit:   1000,
				Marked:    true,
			},
			{
				SeqNr:     45,
				Departure: time.UnixMilli(105),
				Arrival:   time.UnixMilli(115),
				SizeBit:   1000,
				Marked:    true,
			},
		},
	}
	data, err := acks.Marshal()
	require.NoError(t, err)

	for i := range acks.PacketEvents {
		acks.PacketEvents[i].Arrived = true // marshal sets all to arrived
	}

	res, err := UnmarshalFeedback(data)
	require.NoError(t, err)

	require.Equal(t, acks.PacketEvents, res.PacketEvents)
}
