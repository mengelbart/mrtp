package browser

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

type Signaler struct {
	onSessionDescription func(*webrtc.SessionDescription) error
	onICECandidate       func(webrtc.ICECandidateInit) error

	candidatesChan chan webrtc.ICECandidateInit
	remoteDescSet  bool
}

func NewSignaler(
	onSessionDescription func(*webrtc.SessionDescription) error,
	onICECandidate func(webrtc.ICECandidateInit) error,
) *Signaler {
	return &Signaler{
		onSessionDescription: onSessionDescription,
		onICECandidate:       onICECandidate,
		candidatesChan:       make(chan webrtc.ICECandidateInit, 10),
	}
}

func (s *Signaler) HandleSessionDescription(sessionDesc *webrtc.SessionDescription) error {
	if s.onSessionDescription != nil {
		err := s.onSessionDescription(sessionDesc)
		if err != nil {
			return err
		}
		if !s.remoteDescSet {
			s.remoteDescSet = true

			for {
				select {
				case candidate := <-s.candidatesChan:
					if s.remoteDescSet && s.onICECandidate != nil {
						return s.onICECandidate(candidate)
					}
				default:
					return nil
				}
			}
		}
	}
	return fmt.Errorf("onSessionDescription handler not set")
}

func (s *Signaler) HandleICECandidate(candidate webrtc.ICECandidateInit) error {
	if s.remoteDescSet && s.onICECandidate != nil {
		return s.onICECandidate(candidate)
	}

	select {
	case s.candidatesChan <- candidate:
		return nil
	default:
		return fmt.Errorf("candidates channel is full, dropping candidate: %s", candidate.Candidate)
	}
}
