package output

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Device is a CoreAudio output device reported by the airtoothaudio helper.
type Device struct {
	UID       string
	Transport string // builtin, bluetooth, usb, hdmi, airplay, ...
	Name      string
}

// BT renders PCM to a CoreAudio output device (e.g. a paired Bluetooth soundbar)
// by piping it to the airtoothaudio helper in render mode.
type BT struct {
	dev   Device
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func NewBT(dev Device) *BT { return &BT{dev: dev} }

func (b *BT) Name() string { return b.dev.Name }

// Prestart spawns the render helper and returns its PID so the capture tap can
// exclude it (avoiding a feedback loop). Run reuses this process.
func (b *BT) Prestart(ctx context.Context) (int, error) {
	bin, err := helperPath()
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, bin, "render", "--device-uid", b.dev.UID)
	cmd.WaitDelay = 3 * time.Second // force-kill if it lingers after ctx cancel
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("bt %s: starting helper: %w", b.dev.Name, err)
	}
	b.cmd, b.stdin = cmd, stdin
	return cmd.Process.Pid, nil
}

func (b *BT) Run(ctx context.Context, in <-chan []byte) error {
	if b.cmd == nil { // not prestarted: start now
		if _, err := b.Prestart(ctx); err != nil {
			return err
		}
	}
	log.Printf("bt: rendering to %s (%s)", b.dev.Name, b.dev.UID)

	stop := func() error {
		b.stdin.Close()
		if b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
		_ = b.cmd.Wait()
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return stop()
		case chunk, ok := <-in:
			if !ok {
				return stop()
			}
			if _, err := b.stdin.Write(chunk); err != nil {
				return stop() // helper exited
			}
		}
	}
}

// ListOutputDevices returns the CoreAudio output devices the helper can see.
func ListOutputDevices(ctx context.Context) ([]Device, error) {
	bin, err := helperPath()
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, bin, "list").Output()
	if err != nil {
		return nil, fmt.Errorf("listing output devices: %w", err)
	}

	var devices []Device
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		devices = append(devices, Device{UID: parts[0], Transport: parts[1], Name: parts[2]})
	}
	return devices, sc.Err()
}

// helperPath locates the airtoothaudio binary: $airtooth_AUDIO, then the built
// package under the working directory, then $PATH.
func helperPath() (string, error) {
	if p := os.Getenv("airtooth_AUDIO"); p != "" {
		return p, nil
	}
	local := filepath.Join("native", "airtoothaudio", ".build", "release", "airtoothaudio")
	if _, err := os.Stat(local); err == nil {
		return filepath.Abs(local)
	}
	if p, err := exec.LookPath("airtoothaudio"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("airtoothaudio helper not found (run `make deps`, set $airtooth_AUDIO, or put it on $PATH)")
}
