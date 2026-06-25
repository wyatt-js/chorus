// Package pipeline wires system-audio capture to one or more time-offset outputs.
package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
	"github.com/wyattjs/chorus/internal/capture"
	"github.com/wyattjs/chorus/internal/output"
)

// Target is an output plus the delay applied to its stream.
type Target struct {
	Output output.Output
	Offset time.Duration
}

// Options configures a play session.
type Options struct {
	Targets []Target
}

// Session is a running capture→fan-out pipeline whose set of outputs can change
// while it plays. The capture tap is restarted only when an output that renders
// locally (Bluetooth/AirPlay) is added, so its process can be excluded from the
// tap; everything else is added/removed seamlessly.
type Session struct {
	b *output.Broadcaster

	mu        sync.Mutex
	rootCtx   context.Context
	cap       *capture.Capture
	capCancel context.CancelFunc
	pids      map[string]int // output name -> excluded PID (local-render outputs)
}

// NewSession creates an idle session that produces the given PCM format.
func NewSession(format audio.Format) *Session {
	return &Session{b: output.New(format), pids: map[string]int{}}
}

// Start prestarts local-render outputs, brings up the capture tap (excluding
// their processes), and begins fanning audio out to the initial targets.
func (s *Session) Start(ctx context.Context, targets []Target) error {
	if len(targets) == 0 {
		return errors.New("no outputs selected")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rootCtx = ctx
	for _, t := range targets {
		if err := s.prestart(ctx, t.Output); err != nil {
			return err
		}
		s.b.Add(t.Output, t.Offset)
	}
	s.b.Start(ctx)
	return s.restartCaptureLocked(ctx)
}

// Apply removes the named outputs and adds the given targets. If any added
// output renders locally, the capture tap is restarted to exclude it (a brief
// gap for all outputs; none are disconnected). Removals and Cast adds are
// seamless.
func (s *Session) Apply(ctx context.Context, add []Target, removeNames []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range removeNames {
		s.b.RemoveSink(name)
		delete(s.pids, name)
	}
	tapRestart := false
	for _, t := range add {
		if _, ok := t.Output.(output.Prestarter); ok {
			if err := s.prestart(s.rootCtx, t.Output); err != nil {
				return err
			}
			tapRestart = true
		}
		s.b.AddSink(t.Output, t.Offset)
	}
	if tapRestart {
		return s.restartCaptureLocked(s.rootCtx)
	}
	return nil
}

// SetOffset retunes a live output's delay (absolute, relative to the others)
// without interrupting playback — used by acoustic calibration to time-align.
func (s *Session) SetOffset(name string, target time.Duration) {
	s.b.SetOffset(name, target)
}

// Offset returns the delay currently applied to the named output.
func (s *Session) Offset(name string) time.Duration { return s.b.Offset(name) }

// Probe plays a calibration tone on one output and silences the rest, returning
// the moment the tone was emitted. window must outlast the slowest output's
// latency so the tone is still emitted while the others stay quiet.
func (s *Session) Probe(name string, pcm []byte, window time.Duration) (time.Time, error) {
	return s.b.Probe(name, pcm, window)
}

// Wait blocks until the session's context is cancelled, then tears down capture.
func (s *Session) Wait() error {
	<-s.rootCtx.Done()
	s.mu.Lock()
	s.stopCaptureLocked()
	s.mu.Unlock()
	return nil
}

// prestart starts an output's local render process (if any) and records its PID
// so the capture tap can exclude it.
func (s *Session) prestart(ctx context.Context, out output.Output) error {
	p, ok := out.(output.Prestarter)
	if !ok {
		return nil
	}
	pid, err := p.Prestart(ctx)
	if err != nil {
		return err
	}
	if pid > 0 {
		s.pids[out.Name()] = pid
	}
	return nil
}

func (s *Session) pidList() []int {
	pids := make([]int, 0, len(s.pids))
	for _, p := range s.pids {
		pids = append(pids, p)
	}
	return pids
}

// restartCaptureLocked stops any current tap and starts a fresh one excluding all
// current local-render PIDs, wiring it to the persistent fan-out. Caller holds mu.
func (s *Session) restartCaptureLocked(parent context.Context) error {
	s.stopCaptureLocked()

	capCtx, cancel := context.WithCancel(parent)
	c, err := capture.Start(capCtx, s.pidList())
	if err != nil {
		cancel()
		return err
	}
	s.cap, s.capCancel = c, cancel
	go func() { _ = s.b.Feed(capCtx, c.PCM) }()
	go func() { <-capCtx.Done(); c.Stop() }()
	return nil
}

func (s *Session) stopCaptureLocked() {
	if s.capCancel == nil {
		return
	}
	s.capCancel()
	s.cap.Stop()
	_ = s.cap.Wait()
	s.cap, s.capCancel = nil, nil
}

// Run captures system audio and fans it out to a fixed set of targets until ctx
// is cancelled. It's the non-interactive entry point (flag-driven / non-TTY).
func Run(ctx context.Context, opts Options) error {
	s := NewSession(audio.StereoCD)
	if err := s.Start(ctx, opts.Targets); err != nil {
		return err
	}
	return s.Wait()
}
