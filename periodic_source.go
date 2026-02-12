package mrtp

import (
	"time"
)

type PeriodicSourceError struct {
	nextAvailable time.Time
}

func (e PeriodicSourceError) Error() string {
	return "no data available"
}

type periodicSource struct {
	src      Source
	period   time.Duration
	lastRead time.Time
}

func NewPeriodicSource(src Source, period time.Duration) *periodicSource {
	ps := &periodicSource{
		src:      src,
		period:   period,
		lastRead: time.Time{},
	}
	return ps
}

func (s *periodicSource) Read(buf []byte) (int, error) {
	if s.lastRead.IsZero() || time.Since(s.lastRead) >= s.period {
		s.lastRead = time.Now()
		return s.src.Read(buf)
	}
	return 0, PeriodicSourceError{
		nextAvailable: s.lastRead.Add(s.period),
	}
}

func (s *periodicSource) Close() error {
	return s.src.Close()
}
