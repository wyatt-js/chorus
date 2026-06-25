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
	s := &sink{out: fakeOutput{name: "x"}, ch: make(chan []byte, 16)}
	b.sinks = append(b.sinks, *s)

	// Add 30ms (3 chunks) of delay: each subsequent real chunk should be preceded
	// by one injected silence chunk until the backlog clears.
	b.SetOffset("x", 30*time.Millisecond)
	if got := b.sinks[0].pendingSilence; got != 3 {
		t.Fatalf("pendingSilence = %d, want 3", got)
	}
	real := []byte{1, 2, 3, 4}
	for range 3 {
		b.deliver(&b.sinks[0], real)
	}
	if got := b.sinks[0].pendingSilence; got != 0 {
		t.Errorf("pendingSilence after draining = %d, want 0", got)
	}
	// Channel should hold 3 (silence, real) pairs = 6 chunks, alternating.
	if got := len(b.sinks[0].ch); got != 6 {
		t.Fatalf("queued chunks = %d, want 6", got)
	}
	for i := range 3 {
		if sil := <-b.sinks[0].ch; len(sil) != ChunkFrames*audio.StereoCD.BytesPerFrame() {
			t.Errorf("pair %d: first chunk not a full silence chunk (len %d)", i, len(sil))
		}
		if got := <-b.sinks[0].ch; string(got) != string(real) {
			t.Errorf("pair %d: second chunk = %v, want real audio", i, got)
		}
	}

	// Now pull the sink 20ms (2 chunks) earlier: the next two real chunks are
	// dropped instead of delivered.
	b.SetOffset("x", 10*time.Millisecond)
	if got := b.sinks[0].skip; got != 2 {
		t.Fatalf("skip = %d, want 2", got)
	}
	b.deliver(&b.sinks[0], real)
	b.deliver(&b.sinks[0], real)
	if got := len(b.sinks[0].ch); got != 0 {
		t.Errorf("dropped chunks should not enqueue; queued = %d, want 0", got)
	}
	b.deliver(&b.sinks[0], real)
	if got := len(b.sinks[0].ch); got != 1 {
		t.Errorf("after skip cleared, real chunk should enqueue; queued = %d, want 1", got)
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
