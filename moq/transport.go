package moq

import (
	"github.com/mengelbart/moqtransport"
)

type Option func(*Transport) error

type Transport struct {
	conn    moqtransport.Connection
	session *moqtransport.Session
}

func New(conn moqtransport.Connection, opts ...Option) (*Transport, error) {
	t := &Transport{
		conn: conn,
	}
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}
	session := &moqtransport.Session{
		InitialMaxRequestID: 100,
		Handler:             t,
		Qlogger:             nil,
	}
	go func() {
		err := session.Run(conn)
		if err != nil {
			// TODO: Handle error and close transport and signal to users?
			panic(err)
		}
	}()
	return t, nil
}

func (t *Transport) Handle(w moqtransport.ResponseWriter, m *moqtransport.Message) {
	switch m.Method {
	case moqtransport.MessageSubscribe:
	case moqtransport.MessageFetch:
	case moqtransport.MessageAnnounce:
	case moqtransport.MessageAnnounceCancel:
	case moqtransport.MessageUnannounce:
	case moqtransport.MessageTrackStatusRequest:
	case moqtransport.MessageTrackStatus:
	case moqtransport.MessageGoAway:
	case moqtransport.MessageSubscribeAnnounces:
	case moqtransport.MessageUnsubscribeAnnounces:
	}
}
