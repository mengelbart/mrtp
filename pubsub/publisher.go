package pubsub

type Publisher struct {
	broker *Broker
}

func NewPublisher(b *Broker) *Publisher {
	return &Publisher{
		broker: b,
	}
}

func (p *Publisher) Announce(topic string) error {
	return p.broker.announce(topic)
}

func (p *Publisher) Publish(topic string, m Message) error {
	return p.broker.publish(topic, m)
}
