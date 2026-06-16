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

// AirPlayDevice is an AirPlay 2 receiver reported by the airplayrelay sidecar.
type AirPlayDevice struct {
	ID    string // stable receiver id (from its mDNS TXT record)
	Name  string // friendly name, e.g. "Living Room HomePod"
	Addr  string // host:port (informational)
	Proto string // "airplay2" or "airplay1"
}

// AirPlay streams PCM to an AirPlay 2 receiver by piping it to the airplayrelay
// sidecar in render mode (which wraps airplay2-rs: HomeKit pairing + live PCM).
type AirPlay struct {
	dev   AirPlayDevice
	pin   string
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// NewAirPlay builds an AirPlay output for dev. pin is optional; supply it only
// for receivers that require a PIN on first pairing (it is ignored once paired).
func NewAirPlay(dev AirPlayDevice, pin string) *AirPlay { return &AirPlay{dev: dev, pin: pin} }

func (a *AirPlay) Name() string { return a.dev.Name }

// Prestart spawns the render sidecar and returns its PID so the capture tap can
// exclude it (avoiding a feedback loop). Run reuses this process.
func (a *AirPlay) Prestart(ctx context.Context) (int, error) {
	bin, err := airplayHelperPath()
	if err != nil {
		return 0, err
	}
	args := []string{"render", "--device", a.dev.ID}
	if a.pin != "" {
		args = append(args, "--pin", a.pin)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 3 * time.Second // force-kill if it lingers after ctx cancel
	cmd.Stderr = os.Stderr          // surface pairing prompts / progress
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("airplay %s: starting sidecar: %w", a.dev.Name, err)
	}
	a.cmd, a.stdin = cmd, stdin
	return cmd.Process.Pid, nil
}

func (a *AirPlay) Run(ctx context.Context, in <-chan []byte) error {
	if a.cmd == nil { // not prestarted: start now
		if _, err := a.Prestart(ctx); err != nil {
			return err
		}
	}
	log.Printf("airplay: streaming to %s (%s)", a.dev.Name, a.dev.ID)

	stop := func() error {
		a.stdin.Close()
		if a.cmd.Process != nil {
			_ = a.cmd.Process.Kill()
		}
		_ = a.cmd.Wait()
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
			if _, err := a.stdin.Write(chunk); err != nil {
				return stop() // sidecar exited
			}
		}
	}
}

// ListAirPlayDevices returns the AirPlay receivers the sidecar can see.
func ListAirPlayDevices(ctx context.Context) ([]AirPlayDevice, error) {
	bin, err := airplayHelperPath()
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, bin, "list").Output()
	if err != nil {
		return nil, fmt.Errorf("listing AirPlay devices: %w", err)
	}

	var devices []AirPlayDevice
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		// id \t name \t addr:port \t proto
		parts := strings.SplitN(sc.Text(), "\t", 4)
		if len(parts) != 4 {
			continue
		}
		devices = append(devices, AirPlayDevice{
			ID: parts[0], Name: parts[1], Addr: parts[2], Proto: parts[3],
		})
	}
	return devices, sc.Err()
}

// airplayHelperPath locates the airplayrelay binary: $AIRTOOTH_AIRPLAY, then the
// built crate under the working directory, then $PATH.
func airplayHelperPath() (string, error) {
	if p := os.Getenv("AIRTOOTH_AIRPLAY"); p != "" {
		return p, nil
	}
	local := filepath.Join("native", "airplayrelay", "target", "release", "airplayrelay")
	if _, err := os.Stat(local); err == nil {
		return filepath.Abs(local)
	}
	if p, err := exec.LookPath("airplayrelay"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("airplayrelay sidecar not found (run `make deps`, set $AIRTOOTH_AIRPLAY, or put it on $PATH)")
}
