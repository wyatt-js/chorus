package output

import (
	"context"
	"testing"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
)

// fakeOutput is a minimal Output for exercising the fan-out bookkeeping.
type fakeOutput struct{ name string }

func (f fakeOutput) Name() string                             { return f.name }
func (f fakeOutput) Run(context.Context, <-chan []byte) error { return nil }

func TestSetOffsetAdjustsLiveSink(t *testing.T) {
	b := New(audio.StereoCD)
	b.sinks = append(b.sinks, sink{out: fakeOutput{name: "x"}, ch: make(chan []byte, 16)})

	// Add 30ms (3 chunks) of delay: one contiguous block of silence is enqueued
	// immediately so the speaker goes quiet, then resumes in sync.
	b.SetOffset("x", 30*time.Millisecond)
	if got := len(b.sinks[0].ch); got != 3 {
		t.Fatalf("queued silence chunks = %d, want 3", got)
	}
	if got := b.Offset("x"); got != 30*time.Millisecond {
		t.Errorf("Offset = %v, want 30ms", got)
	}
	full := ChunkFrames * audio.StereoCD.BytesPerFrame()
	for i := 0; i < 3; i++ {
		if sil := <-b.sinks[0].ch; len(sil) != full {
			t.Errorf("chunk %d is not a full silence chunk (len %d)", i, len(sil))
		}
	}

	// Refill with 5 chunks, then reduce by 20ms (2 chunks): two are dropped to
	// catch up, leaving 3.
	for i := 0; i < 5; i++ {
		b.sinks[0].ch <- make([]byte, full)
	}
	b.SetOffset("x", 10*time.Millisecond)
	if got := len(b.sinks[0].ch); got != 3 {
		t.Errorf("queued chunks after reduce = %d, want 3", got)
	}

	// Negative target clamps to zero.
	b.SetOffset("x", -50*time.Millisecond)
	if got := b.Offset("x"); got != 0 {
		t.Errorf("Offset after negative target = %v, want 0", got)
	}
}

func TestSilenceChunks(t *testing.T) {
	// One chunk is 441 frames = 10ms at 44.1kHz.
	tests := []struct {
		offset time.Duration
		want   int
	}{
		{0, 0},
		{-50 * time.Millisecond, 0},
		{10 * time.Millisecond, 1},
		{100 * time.Millisecond, 10},
		{1 * time.Second, 100},
		{2 * time.Second, 200},
	}
	for _, tt := range tests {
		if got := silenceChunks(tt.offset); got != tt.want {
			t.Errorf("silenceChunks(%v) = %d, want %d", tt.offset, got, tt.want)
		}
	}
}

func TestWAVStreamHeader(t *testing.T) {
	h := wavStreamHeader(audio.StereoCD)
	if len(h) != 44 {
		t.Fatalf("header len = %d, want 44", len(h))
	}
	if string(h[0:4]) != "RIFF" || string(h[8:12]) != "WAVE" || string(h[36:40]) != "data" {
		t.Errorf("bad WAV chunk tags: %q %q %q", h[0:4], h[8:12], h[36:40])
	}
	le32 := func(b []byte) uint32 {
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	}
	// sampleRate at offset 24, byteRate (= 48000*4) at offset 28.
	if got := le32(h[24:]); got != 48000 {
		t.Errorf("sampleRate = %d, want 48000", got)
	}
	if got := le32(h[28:]); got != 192000 {
		t.Errorf("byteRate = %d, want 192000", got)
	}
}
