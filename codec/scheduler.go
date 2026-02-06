package codec

import (
	"math"
	"sync"

	"golang.org/x/time/rate"
)

type Scheduler struct {
	lock       sync.Mutex
	stream     MediaFrameSource
	transport  Writer
	pacer      *rate.Limiter
	targetRate uint
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		lock:   sync.Mutex{},
		stream: nil,
	}
}

func (s *Scheduler) Run() error {
	for {

	}
	return nil
}

func (s *Scheduler) schedule() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	num, den := s.stream.FrameRate()
	fps := float64(num) / float64(den)
	bitsPerFrame := float64(s.targetRate) / fps
	bytesPerFrame := math.Ceil((float64(bitsPerFrame) / 8.0))
	_, err := s.stream.ReadFrame(uint(bytesPerFrame))
	if err != nil {
		return err
	}
	return nil
}

func (s *Scheduler) SetTargetRate(rate uint) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.targetRate = rate
}
