// Package output fans captured PCM out to multiple audio sinks (Google Cast,
// Bluetooth, ...), each with its own start delay for coarse alignment.
package output

import (
	"context"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
)

// LogWriter is where render-sidecar stderr (airplayrelay, chorusaudio) is sent.
// Defaults to the terminal; the interactive player redirects it to a log file so
// sidecar chatter doesn't clutter the TUI.
var LogWriter io.Writer = os.Stderr

// ChunkFrames is the PCM granule the broadcaster reads and forwards (10ms at
// 44.1kHz). It also sets the granularity of per-output offsets.
const ChunkFrames = 441

// Output is a single audio sink. Run consumes PCM chunks (s16le/44100/stereo)
// from in until in is closed or ctx is cancelled, then tears down.
type Output interface {
	Name() string
	Run(ctx context.Context, in <-chan []byte) error
}

// Prestarter is an Output that spawns a local process which itself plays audio
// (e.g. the Bluetooth render helper). Prestart starts that process early so its
// PID can be excluded from the system-audio tap, preventing a feedback loop.
// It returns the PID to exclude (0 if none).
type Prestarter interface {
	Prestart(ctx context.Context) (pid int, err error)
}

// Prestart prepares every output that needs it and returns the PIDs to exclude
// from capture.
func (b *Broadcaster) Prestart(ctx context.Context) ([]int, error) {
	var pids []int
	for _, s := range b.sinks {
		p, ok := s.out.(Prestarter)
		if !ok {
			continue
		}
		pid, err := p.Prestart(ctx)
		if err != nil {
			return nil, err
		}
		if pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// sink couples an Output with its delivery channel, start offset, and a cancel
// that stops just this output (for live removal).
type sink struct {
	out    Output
	ch     chan []byte
	offset time.Duration
	cancel context.CancelFunc
}

// Broadcaster reads PCM from a single source (via Feed) and tees it to every
// registered Output. Outputs can be added and removed while it runs. A slow
// output drops chunks rather than stalling the others.
type Broadcaster struct {
	format  audio.Format
	pcmCh   chan []byte
	mu      sync.Mutex
	sinks   []sink
	rootCtx context.Context
}

func New(format audio.Format) *Broadcaster {
	return &Broadcaster{format: format, pcmCh: make(chan []byte, 16)}
}

// Add registers an output before Start. offset delays this output's stream
// relative to the others by prepending that much silence.
func (b *Broadcaster) Add(out Output, offset time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sinks = append(b.sinks, sink{out: out, offset: offset})
}

// Outputs returns the registered outputs (for logging).
func (b *Broadcaster) Outputs() []Output {
	b.mu.Lock()
	defer b.mu.Unlock()
	outs := make([]Output, len(b.sinks))
	for i, s := range b.sinks {
		outs[i] = s.out
	}
	return outs
}

// Start launches the fan-out and every registered output. ctx is the session
// root: cancelling it stops all outputs.
func (b *Broadcaster) Start(ctx context.Context) {
	b.mu.Lock()
	b.rootCtx = ctx
	for i := range b.sinks {
		b.launchLocked(&b.sinks[i])
	}
	b.mu.Unlock()
	go b.fanOut(ctx)
}

// AddSink adds and starts an output while the session is running.
func (b *Broadcaster) AddSink(out Output, offset time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := sink{out: out, offset: offset}
	b.launchLocked(&s)
	b.sinks = append(b.sinks, s)
}

// RemoveSink stops and drops every output with the given name.
func (b *Broadcaster) RemoveSink(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.sinks[:0]
	for _, s := range b.sinks {
		if s.out.Name() == name {
			if s.cancel != nil {
				s.cancel()
			}
			continue
		}
		kept = append(kept, s)
	}
	b.sinks = kept
}

// launchLocked sizes the sink's channel to hold its offset of priming silence,
// pre-fills it, and starts the output. Caller holds b.mu.
func (b *Broadcaster) launchLocked(s *sink) {
	chunkBytes := ChunkFrames * b.format.BytesPerFrame()
	n := silenceChunks(s.offset)
	// Size the buffer to fit the priming silence plus ~2s headroom so priming
	// never blocks before the output starts draining.
	s.ch = make(chan []byte, n+200)
	for range n {
		s.ch <- make([]byte, chunkBytes)
	}
	sctx, cancel := context.WithCancel(b.rootCtx)
	s.cancel = cancel
	out, ch := s.out, s.ch
	go func() {
		if err := out.Run(sctx, ch); err != nil && sctx.Err() == nil {
			log.Printf("output %s: %v", out.Name(), err)
		}
	}()
}

// Feed reads fixed PCM chunks from src into the fan-out until ctx is cancelled or
// src ends. The Session calls this once per capture tap (again after a tap
// restart), feeding the same persistent fan-out so outputs aren't torn down.
func (b *Broadcaster) Feed(ctx context.Context, src io.Reader) error {
	chunkBytes := ChunkFrames * b.format.BytesPerFrame()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		buf := make([]byte, chunkBytes)
		if _, err := io.ReadFull(src, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF || err == io.ErrClosedPipe {
				return nil
			}
			return err
		}
		select {
		case b.pcmCh <- buf:
		case <-ctx.Done():
			return nil
		}
	}
}

// fanOut tees each PCM chunk to every current sink, dropping for any sink that's
// behind rather than stalling the rest. Each chunk is freshly allocated so sinks
// can safely share the (read-only) slice.
func (b *Broadcaster) fanOut(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case buf := <-b.pcmCh:
			b.mu.Lock()
			for _, s := range b.sinks {
				select {
				case s.ch <- buf:
				default: // sink is behind; drop rather than stall everyone
				}
			}
			b.mu.Unlock()
		}
	}
}

func silenceChunks(offset time.Duration) int {
	if offset <= 0 {
		return 0
	}
	chunkDur := time.Duration(ChunkFrames) * time.Second / 44100
	return int(offset / chunkDur)
}
