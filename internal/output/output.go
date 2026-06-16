// Package output fans captured PCM out to multiple audio sinks (Google Cast,
// Bluetooth, ...), each with its own start delay for coarse alignment.
package output

import (
	"context"
	"io"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
)

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

// sink couples an Output with its delivery channel and start offset.
type sink struct {
	out    Output
	ch     chan []byte
	offset time.Duration
}

// Broadcaster reads PCM from a single source and tees it to every registered
// Output. A slow output is allowed to drop chunks rather than stall the others.
type Broadcaster struct {
	format audio.Format
	sinks  []sink
}

func New(format audio.Format) *Broadcaster { return &Broadcaster{format: format} }

// Add registers an output. offset delays this output's stream relative to the
// others by prepending that much silence.
func (b *Broadcaster) Add(out Output, offset time.Duration) {
	// Buffer ~2s so a briefly-slow output (e.g. Cast connecting) doesn't drop.
	b.sinks = append(b.sinks, sink{out: out, ch: make(chan []byte, 200), offset: offset})
}

// Outputs returns the registered outputs (for logging).
func (b *Broadcaster) Outputs() []Output {
	outs := make([]Output, len(b.sinks))
	for i, s := range b.sinks {
		outs[i] = s.out
	}
	return outs
}

// Run starts every output and pumps src to all of them until ctx is cancelled or
// src ends. It returns the first output error, if any.
func (b *Broadcaster) Run(ctx context.Context, src io.Reader) error {
	chunkBytes := ChunkFrames * b.format.BytesPerFrame()
	errc := make(chan error, len(b.sinks))

	for _, s := range b.sinks {
		// Pre-fill with offset worth of silence to delay this output's audio.
		for i := 0; i < silenceChunks(s.offset); i++ {
			s.ch <- make([]byte, chunkBytes)
		}
		go func(s sink) { errc <- s.out.Run(ctx, s.ch) }(s)
	}

	readErr := pump(ctx, src, chunkBytes, b.sinks)

	// Signal end-of-stream to outputs and collect their results.
	for _, s := range b.sinks {
		close(s.ch)
	}
	for range b.sinks {
		if err := <-errc; err != nil && readErr == nil {
			readErr = err
		}
	}
	return readErr
}

// pump reads fixed chunks from src and fans each out to every sink. Each chunk is
// freshly allocated so sinks can safely share the (read-only) slice.
func pump(ctx context.Context, src io.Reader, chunkBytes int, sinks []sink) error {
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
		for _, s := range sinks {
			select {
			case s.ch <- buf:
			default: // sink is behind; drop this chunk rather than stall everyone
			}
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
