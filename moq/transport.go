package moq

import (
	"context"
	"strings"

	"github.com/mengelbart/moqtransport"
)

type Option func(*Transport) error

type Transport struct {
	conn    moqtransport.Connection
	session *moqtransport.Session

	namespaces map[string]*namespace
}

func New(conn moqtransport.Connection, opts ...Option) (*Transport, error) {
	t := &Transport{
		conn:       conn,
		session:    nil,
		namespaces: map[string]*namespace{},
	}
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}
	t.session = &moqtransport.Session{
		InitialMaxRequestID:    100,
		Handler:                t,
		Qlogger:                nil,
		SubscribeHandler:       t,
		SubscribeUpdateHandler: nil,
	}
	err := t.session.Run(conn)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Handle implements moqtransport.Handler
// TODO: Unexport handler by moving handle func to another type that is not
// exported.
func (t *Transport) Handle(w moqtransport.ResponseWriter, m *moqtransport.Message) {
	switch m.Method {
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

func (t *Transport) HandleSubscribe(w *moqtransport.SubscribeResponseWriter, m *moqtransport.SubscribeMessage) {
	t.handleSubscription(w, m)
}

func (t *Transport) Subscribe(ctx context.Context, namespace []string, trackname string) (*remoteTrack, error) {
	rt, err := t.session.Subscribe(ctx, namespace, trackname)
	if err != nil {
		return nil, err
	}
	return newRemoteTrack(rt)
}

func (t *Transport) AddTrack(namespace []string, trackname string, track *LocalTrack) error {
	ns, ok := t.namespaces[strings.Join(namespace, "")]
	if !ok {
		ns = newNamespace()
		t.namespaces[strings.Join(namespace, "")] = ns
	}
	if err := ns.addTrack(trackname, track); err != nil {
		return err
	}
	return nil
}

func (t *Transport) handleSubscription(w *moqtransport.SubscribeResponseWriter, m *moqtransport.SubscribeMessage) {
	ns, ok := t.namespaces[strings.Join(m.Namespace, "")]
	if !ok {
		w.Reject(moqtransport.ErrorCodeSubscribeTrackDoesNotExist, "track not found")
		return
	}
	track := ns.findTrack(m.Track)
	if t == nil {
		w.Reject(moqtransport.ErrorCodeSubscribeTrackDoesNotExist, "track not found")
		return
	}
	if !ok {
		w.Reject(moqtransport.ErrorCodeSubscribeInternal, "failed to prepare publisher")
		return
	}
	track.subscribe(w)
	w.Accept()
}
