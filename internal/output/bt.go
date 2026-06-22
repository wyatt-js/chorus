package output

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Device is a CoreAudio output device reported by the chorusaudio helper.
type Device struct {
	UID       string
	Transport string // builtin, bluetooth, usb, hdmi, airplay, ...
	Name      string
}

// BluetoothDevice is a paired Bluetooth audio device reported by the chorusaudio
// helper (via IOBluetooth). Unlike Device, it exists whether or not the device is
// currently connected — connecting it is what makes it a CoreAudio output.
type BluetoothDevice struct {
	Address   string // hardware address, e.g. "70-8c-f2-87-20-18"
	Name      string
	Connected bool
}

// BT renders PCM to a CoreAudio output device (e.g. a paired Bluetooth soundbar)
// by piping it to the chorusaudio helper in render mode.
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
	cmd.Stderr = LogWriter
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

// ResolveOutputByName finds a CoreAudio output device by (case-insensitive)
// name. Used to map a just-connected Bluetooth device to its renderable UID.
func ResolveOutputByName(ctx context.Context, name string) (Device, bool, error) {
	outs, err := ListOutputDevices(ctx)
	if err != nil {
		return Device{}, false, err
	}
	for _, d := range outs {
		if strings.EqualFold(d.Name, name) {
			return d, true, nil
		}
	}
	return Device{}, false, nil
}

// ListBluetoothDevices returns the paired Bluetooth audio devices the helper can
// see, each annotated with whether it is currently connected. When probe > 0,
// disconnected devices are pinged with a baseband name request (per-device page
// timeout = probe) and only the reachable ones — powered on and in range — are
// returned; long-stale pairings for absent devices are hidden. Pinging is
// serialized by the controller, so a list with several absent devices can take
// several seconds.
func ListBluetoothDevices(ctx context.Context, probe time.Duration) ([]BluetoothDevice, error) {
	bin, err := helperPath()
	if err != nil {
		return nil, err
	}
	args := []string{"bt-list"}
	if probe > 0 {
		args = append(args, "--reachable-timeout", strconv.FormatFloat(probe.Seconds(), 'f', -1, 64))
	}
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("listing Bluetooth devices: %w", err)
	}

	var devices []BluetoothDevice
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		devices = append(devices, BluetoothDevice{
			Address:   parts[0],
			Connected: parts[1] == "1",
			Name:      parts[2],
		})
	}
	return devices, sc.Err()
}

// ConnectBluetooth opens a connection to a paired Bluetooth device by address so
// it comes online as a CoreAudio output. It blocks until connected or fails.
func ConnectBluetooth(ctx context.Context, address string) error {
	bin, err := helperPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "bt-connect", "--address", address)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// DisconnectBluetooth drops the OS-level connection to a paired Bluetooth device
// by address, so it stops being a CoreAudio output. A no-op if already off.
func DisconnectBluetooth(ctx context.Context, address string) error {
	bin, err := helperPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "bt-disconnect", "--address", address)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// helperPath locates the chorusaudio binary: $CHORUS_AUDIO, then the built
// package under the working directory, then $PATH.
func helperPath() (string, error) {
	if p := os.Getenv("CHORUS_AUDIO"); p != "" {
		return p, nil
	}
	local := filepath.Join("native", "chorusaudio", ".build", "release", "chorusaudio")
	if _, err := os.Stat(local); err == nil {
		return filepath.Abs(local)
	}
	if p, err := exec.LookPath("chorusaudio"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("chorusaudio helper not found (run `make deps`, set $CHORUS_AUDIO, or put it on $PATH)")
}
