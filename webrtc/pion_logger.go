package webrtc

import (
	"fmt"
	"log/slog"
)

type pionLogger struct {
	sl *slog.Logger
}

// Debug implements logging.LeveledLogger.
func (p *pionLogger) Debug(msg string) {
	p.sl.Debug("pion-debug-log", "msg", msg)
}

// Debugf implements logging.LeveledLogger.
func (p *pionLogger) Debugf(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	fmt.Println(s)
	p.sl.Debug("pion-debug-log", "msg", s)
}

// Error implements logging.LeveledLogger.
func (p *pionLogger) Error(msg string) {
	p.sl.Error("pion-error-log", "msg", msg)
}

// Errorf implements logging.LeveledLogger.
func (p *pionLogger) Errorf(format string, args ...any) {
	p.sl.Error("pion-error-log", "msg", fmt.Sprintf(format, args...))
}

// Info implements logging.LeveledLogger.
func (p *pionLogger) Info(msg string) {
	p.sl.Info("pion-info-log", "msg", msg)
}

// Infof implements logging.LeveledLogger.
func (p *pionLogger) Infof(format string, args ...any) {
	p.sl.Info("pion-info-log", "msg", fmt.Sprintf(format, args...))
}

// Trace implements logging.LeveledLogger.
func (p *pionLogger) Trace(msg string) {
	p.sl.Debug("pion-debug-log", "msg", msg)
}

// Tracef implements logging.LeveledLogger.
func (p *pionLogger) Tracef(format string, args ...any) {
	// TODO(ME): This is very brittle and depends on the logging string of GCC.
	// We should replace it once pion supports json as logging format.
	if len(args) == 4 {
		p.sl.Debug(
			"pion-trace-log",
			"seqnr", args[0],
			"arrived", args[1],
			"departure", args[2],
			"arrival", args[3],
		)
	} else if len(args) == 5 {
		rtt, delivered, lossTarget, delayTarget, target := args[0], args[1], args[2], args[3], args[4]
		p.sl.Debug(
			"pion-trace-log",
			"rtt", rtt,
			"delivered", delivered,
			"loss-target", lossTarget,
			"delay-target", delayTarget,
			"target", target,
		)
	} else if len(args) == 11 {
		seq, size, interArrivalTime, interDepartureTime, interGroupDelay, estimate, threshold, usage, state := args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10]
		p.sl.Debug(
			"pion-trace-log",
			"seq", seq,
			"size", size,
			"interArrivalTime", interArrivalTime,
			"interDepartureTime", interDepartureTime,
			"interGroupDelay", interGroupDelay,
			"estimate", estimate,
			"threshold", threshold,
			"usage", usage,
			"state", state,
		)
	} else {
		p.sl.Debug("pion-trace-log", "msg", fmt.Sprintf(format, args...), "len", len(args))
	}
}

// Warn implements logging.LeveledLogger.
func (p *pionLogger) Warn(msg string) {
	p.sl.Warn("pion-warn-log", "msg", msg)
}

// Warnf implements logging.LeveledLogger.
func (p *pionLogger) Warnf(format string, args ...any) {
	p.sl.Warn("pion-warn-log", "msg", fmt.Sprintf(format, args...))
}
