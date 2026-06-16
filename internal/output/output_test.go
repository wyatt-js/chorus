package output

import (
	"testing"
	"time"

	"github.com/wyattjs/airtooth-sync/internal/audio"
)

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
	// sampleRate at offset 24, byteRate (= 44100*4) at offset 28.
	if got := le32(h[24:]); got != 44100 {
		t.Errorf("sampleRate = %d, want 44100", got)
	}
	if got := le32(h[28:]); got != 176400 {
		t.Errorf("byteRate = %d, want 176400", got)
	}
}
