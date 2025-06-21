package relay

import "github.com/mengelbart/mrtp/pubsub"

type Channel struct {
	broker *pubsub.Broker
}

func NewChannel() *Channel {
	return &Channel{
		broker: pubsub.NewBroker(),
	}
}
