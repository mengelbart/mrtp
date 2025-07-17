package relay

type Subscriber interface {
	Write(Object)
}

type Object struct {
}

type subscribeRequest struct {
	s      Subscriber
	result chan int
}

type Track struct {
	name            string
	nextID          int
	subscribers     map[int]Subscriber
	queue           chan Object
	subscriberQueue chan subscribeRequest
}

func newTrack(name string) *Track {
	return &Track{
		name:            name,
		nextID:          0,
		subscribers:     map[int]Subscriber{},
		queue:           make(chan Object),
		subscriberQueue: make(chan subscribeRequest),
	}
}

func (t *Track) loop() {
	for {
		select {
		case o := <-t.queue:
			for _, s := range t.subscribers {
				s.Write(o)
			}
		case s := <-t.subscriberQueue:
			id := t.nextID
			t.nextID++
			t.subscribers[id] = s.s
			s.result <- id
		}
	}
}

func (t *Track) Subscribe(s Subscriber) int {
	res := make(chan int)
	t.subscriberQueue <- subscribeRequest{
		s:      s,
		result: res,
	}
	return <-res
}

func (t *Track) Unsubscribe(int) {

}

func (t *Track) Publish(msg Object) {

}
