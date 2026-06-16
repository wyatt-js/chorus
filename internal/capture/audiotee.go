// Package capture wraps the audiotee Swift sidecar, which taps macOS system
// audio (Core Audio process taps) and writes raw PCM to its stdout.
package capture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
)

// Capture is a running audiotee process. Read PCM from PCM; the bytes are
// interleaved little-endian samples in Format.
type Capture struct {
	cmd *exec.Cmd
	PCM io.ReadCloser
	// Format is what we ask audiotee to produce (44.1kHz/16-bit/stereo).
	Format audio.Format
}

// Start launches audiotee producing 44.1kHz/16-bit/stereo PCM on stdout.
// excludePIDs are processes to keep out of the tap — critically, our own
// Bluetooth render helper, so its output isn't re-captured into a feedback loop.
// audiotee's logs and the first-run permission prompt go to stderr.
func Start(ctx context.Context, excludePIDs []int) (*Capture, error) {
	bin, err := findAudiotee()
	if err != nil {
		return nil, err
	}

	args := []string{"--sample-rate", "44100", "--stereo"}
	if len(excludePIDs) > 0 {
		args = append(args, "--exclude-processes")
		for _, pid := range excludePIDs {
			args = append(args, strconv.Itoa(pid))
		}
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 3 * time.Second // force-kill if it lingers after ctx cancel
	cmd.Stderr = os.Stderr          // surface logs + the macOS capture-permission prompt

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("capture: starting audiotee: %w", err)
	}

	return &Capture{cmd: cmd, PCM: stdout, Format: audio.StereoCD}, nil
}

// Wait blocks until audiotee exits and returns its exit error, if any.
func (c *Capture) Wait() error { return c.cmd.Wait() }

// Stop kills the audiotee process. Safe to call more than once.
func (c *Capture) Stop() {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// findAudiotee locates the audiotee binary: $CHORUS_AUDIOTEE, then the built
// submodule under the working directory, then $PATH.
func findAudiotee() (string, error) {
	if p := os.Getenv("CHORUS_AUDIOTEE"); p != "" {
		return p, nil
	}
	local := filepath.Join("third_party", "audiotee", ".build", "release", "audiotee")
	if _, err := os.Stat(local); err == nil {
		return filepath.Abs(local)
	}
	if p, err := exec.LookPath("audiotee"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("capture: audiotee not found (run `make deps`, set $CHORUS_AUDIOTEE, or put it on $PATH)")
}
