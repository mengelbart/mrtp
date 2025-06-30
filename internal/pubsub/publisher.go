package pubsub

type Publisher struct {
	channel *Channel
}

func newPublisher(c *Channel) *Publisher {
	return &Publisher{
		channel: c,
	}
}

func (p *Publisher) Announce(stream string) error {
	return p.channel.announce(stream)
}

func (p *Publisher) Publish(stream string, m Message) error {
	return p.channel.publish(stream, m)
}
