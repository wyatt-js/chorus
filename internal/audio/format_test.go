package audio

import "testing"

func TestBytesPerFrame(t *testing.T) {
	tests := []struct {
		name string
		f    Format
		want int
	}{
		{"stereo 16-bit", StereoCD, 4},
		{"mono 16-bit", Format{44100, 1, 16}, 2},
		{"stereo 32-bit", Format{48000, 2, 32}, 8},
	}
	for _, tt := range tests {
		if got := tt.f.BytesPerFrame(); got != tt.want {
			t.Errorf("%s: BytesPerFrame() = %d, want %d", tt.name, got, tt.want)
		}
	}
}
