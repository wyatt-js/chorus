package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/wyattjs/chorus/internal/audio"
	"github.com/wyattjs/chorus/internal/discover"
	"github.com/wyattjs/chorus/internal/output"
	"github.com/wyattjs/chorus/internal/pipeline"
)

func playCmd() *cobra.Command {
	var (
		casts    []string
		bts      []string
		airplays []string
		offsets  []string
		volume   int
		pin      string
		wait     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "play",
		Short: "Capture system audio and stream it to Cast, AirPlay, and/or Bluetooth outputs",
		Long: "Capture system audio and fan it out to one or more outputs.\n\n" +
			"Run with no device flags in a terminal to open an interactive picker\n" +
			"that scans for and lets you multi-select Cast, AirPlay, and Bluetooth\n" +
			"devices. Pass --cast/--airplay/--bt to skip the picker.\n\n" +
			"Examples:\n" +
			"  chorus play                       # interactive device picker\n" +
			"  chorus play --cast \"The Frame\"\n" +
			"  chorus play --airplay \"HomePod\"\n" +
			"  chorus play --cast \"The Frame\" --bt \"HW-S700D\"\n" +
			"  chorus play --airplay \"HomePod\" --bt \"HW-S700D\" --offset HW-S700D=2s",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			offMap, err := parseOffsets(offsets)
			if err != nil {
				return err
			}

			var targets []pipeline.Target

			// No device flags: open the interactive picker (or error if not a TTY).
			if len(casts) == 0 && len(bts) == 0 && len(airplays) == 0 {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("select at least one output with --cast, --airplay, and/or --bt, or run `chorus play` in a terminal to pick interactively")
				}
				return playInteractive(ctx, wait, volume, pin, offMap)
			}

			if len(casts) > 0 {
				devs, err := discover.BrowseCast(ctx, wait)
				if err != nil {
					return err
				}
				for _, name := range casts {
					dev, err := matchCast(devs, name)
					if err != nil {
						return err
					}
					targets = append(targets, castTarget(dev, volume, offMap))
				}
			}

			if len(airplays) > 0 {
				devs, err := output.ListAirPlayDevices(ctx)
				if err != nil {
					return err
				}
				for _, name := range airplays {
					dev, err := matchAirPlay(devs, name)
					if err != nil {
						return err
					}
					targets = append(targets, airplayTarget(dev, pin, offMap))
				}
			}

			if len(bts) > 0 {
				outs, err := output.ListOutputDevices(ctx)
				if err != nil {
					return err
				}
				for _, name := range bts {
					dev, err := matchOutput(outs, name)
					if err != nil {
						return err
					}
					targets = append(targets, btTarget(dev, offMap))
				}
			}

			return pipeline.Run(ctx, pipeline.Options{Targets: targets})
		},
	}

	cmd.Flags().StringArrayVar(&casts, "cast", nil, "Cast device name (substring); repeatable")
	cmd.Flags().StringArrayVar(&airplays, "airplay", nil, "AirPlay 2 receiver name (substring); repeatable")
	cmd.Flags().StringArrayVar(&bts, "bt", nil, "Bluetooth/output device name (substring); repeatable")
	cmd.Flags().StringArrayVar(&offsets, "offset", nil, "per-device delay, e.g. --offset HW-S700D=2s; repeatable")
	cmd.Flags().IntVar(&volume, "volume", -1, "Cast volume 0-100 (default: leave device unchanged)")
	cmd.Flags().StringVar(&pin, "pin", "", "AirPlay pairing PIN (only needed the first time a receiver requires one)")
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Second, "how long to look for Cast devices")
	return cmd
}

// castTarget, airplayTarget, and btTarget build a pipeline.Target from a
// discovered device, applying any matching per-device offset. Shared by the
// flag-driven and interactive selection paths.
func castTarget(dev discover.CastDevice, volume int, offMap map[string]time.Duration) pipeline.Target {
	return pipeline.Target{
		Output: output.NewCast(dev, audio.StereoCD, volume),
		Offset: offsetFor(offMap, dev.Name),
	}
}

func airplayTarget(dev output.AirPlayDevice, pin string, offMap map[string]time.Duration) pipeline.Target {
	return pipeline.Target{
		Output: output.NewAirPlay(dev, pin),
		Offset: offsetFor(offMap, dev.Name),
	}
}

func btTarget(dev output.Device, offMap map[string]time.Duration) pipeline.Target {
	return pipeline.Target{
		Output: output.NewBT(dev),
		Offset: offsetFor(offMap, dev.Name),
	}
}

// activeEntry tracks a currently-playing device so the menu can diff against it.
type activeEntry struct {
	name   string // Output.Name(), used to remove the sink
	btAddr string // non-empty => Bluetooth, for OS-level disconnect on deselect
}

// targetForItem turns a chosen menu item into a pipeline target, connecting a
// Bluetooth device first (with the spinner). The returned address is non-empty
// only for Bluetooth, so it can be OS-disconnected later.
func targetForItem(ctx context.Context, it *menuItem, volume int, pin string, offMap map[string]time.Duration) (pipeline.Target, string, error) {
	switch {
	case it.cast != nil:
		return castTarget(*it.cast, volume, offMap), "", nil
	case it.airplay != nil:
		return airplayTarget(*it.airplay, pin, offMap), "", nil
	case it.bt != nil:
		dev, err := connectBluetooth(ctx, *it.bt)
		if err != nil {
			return pipeline.Target{}, "", err
		}
		return btTarget(dev, offMap), it.bt.Address, nil
	}
	return pipeline.Target{}, "", fmt.Errorf("unknown device")
}

// playInteractive runs the first device pick, starts the session, then hands off
// to the in-session control loop (reopen menu, sync, quit).
func playInteractive(parent context.Context, wait time.Duration, volume int, pin string, offMap map[string]time.Duration) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Keep the TUI clean: send Go logs and sidecar stderr to a log file.
	if _, closeLogs := quietLogs(); closeLogs != nil {
		defer closeLogs()
	}

	// First pick, with the animated banner running until discovery finishes.
	done := make(chan struct{})
	var groups []menuGroup
	go func() { groups = discoverAll(ctx, wait); close(done) }()
	animateBanner(done)

	chosen, confirmed, err := selectDevices(groups, nil)
	if err != nil {
		return err
	}
	if !confirmed || len(chosen) == 0 {
		return fmt.Errorf("no devices selected")
	}

	active := map[string]activeEntry{}
	var targets []pipeline.Target
	for _, it := range chosen {
		t, addr, err := targetForItem(ctx, it, volume, pin, offMap)
		if err != nil {
			return err
		}
		targets = append(targets, t)
		active[it.key] = activeEntry{name: t.Output.Name(), btAddr: addr}
	}

	sess := pipeline.NewSession(audio.StereoCD)
	if err := sess.Start(ctx, targets); err != nil {
		return err
	}
	return controlLoop(ctx, cancel, sess, active, wait, volume, pin, offMap)
}

// controlLoop reads single-key commands while audio streams: m = reopen the
// device menu, s = synchronize, q/Ctrl-C = quit. The session keeps playing the
// whole time.
func controlLoop(ctx context.Context, cancel context.CancelFunc, sess *pipeline.Session, active map[string]activeEntry, wait time.Duration, volume int, pin string, offMap map[string]time.Duration) error {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return sess.Wait() // no key control; just stream until cancelled
	}
	defer term.Restore(fd, old)

	printStatus(active)
	buf := make([]byte, 8)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			cancel()
			return sess.Wait()
		}
		switch buf[0] {
		case 'q', 3: // q, Ctrl-C
			cancel()
			return sess.Wait()
		case 'm':
			handleMenu(ctx, sess, active, wait, volume, pin, offMap)
			printStatus(active)
		case 's':
			fmt.Print("\r\n  " + ansiDim + "synchronize: calibration (P2) not yet implemented" + ansiReset + "\r\n")
			printStatus(active)
		}
	}
}

// handleMenu reopens the picker pre-selected with the active set, diffs the
// result, and applies it: deselected devices are disconnected (Bluetooth fully),
// newly selected ones are added (unchanged ones keep playing untouched).
func handleMenu(ctx context.Context, sess *pipeline.Session, active map[string]activeEntry, wait time.Duration, volume int, pin string, offMap map[string]time.Duration) {
	done := make(chan struct{})
	var groups []menuGroup
	stop := startSpinner("rescanning devices…")
	go func() { groups = discoverAll(ctx, wait); close(done) }()
	<-done
	stop("")

	preselect := make(map[string]bool, len(active))
	for k := range active {
		preselect[k] = true
	}

	chosen, confirmed, err := selectDevices(groups, preselect)
	if err != nil {
		fmt.Printf("\r\n  menu error: %v\r\n", err)
		return
	}
	if !confirmed {
		return // cancelled; leave the active set unchanged
	}

	selected := make(map[string]*menuItem, len(chosen))
	for _, it := range chosen {
		selected[it.key] = it
	}

	// Removals: active keys no longer selected.
	var removeNames, removeAddrs []string
	for k, e := range active {
		if _, keep := selected[k]; keep {
			continue
		}
		removeNames = append(removeNames, e.name)
		if e.btAddr != "" {
			removeAddrs = append(removeAddrs, e.btAddr)
		}
		delete(active, k)
	}

	// Additions: selected keys not already active.
	var addTargets []pipeline.Target
	for k, it := range selected {
		if _, exists := active[k]; exists {
			continue
		}
		t, addr, err := targetForItem(ctx, it, volume, pin, offMap)
		if err != nil {
			fmt.Printf("\r\n  could not add %s: %v\r\n", it.label, err)
			continue
		}
		addTargets = append(addTargets, t)
		active[k] = activeEntry{name: t.Output.Name(), btAddr: addr}
	}

	if err := sess.Apply(ctx, addTargets, removeNames); err != nil {
		fmt.Printf("\r\n  applying changes failed: %v\r\n", err)
	}
	for _, addr := range removeAddrs {
		if err := output.DisconnectBluetooth(ctx, addr); err != nil {
			fmt.Printf("\r\n  disconnect failed: %v\r\n", err)
		}
	}
}

// printStatus shows the playback status bar and key hints.
func printStatus(active map[string]activeEntry) {
	names := make([]string, 0, len(active))
	for _, e := range active {
		names = append(names, e.name)
	}
	sort.Strings(names)
	fmt.Printf("\r\n%s▶%s playing to %d device(s): %s\r\n   %s[m]%s menu   %s[s]%s sync   %s[q]%s quit\r\n",
		ansiGreen, ansiReset, len(names), strings.Join(names, ", "),
		ansiBold, ansiReset, ansiBold, ansiReset, ansiBold, ansiReset)
}

func matchCast(devs []discover.CastDevice, name string) (discover.CastDevice, error) {
	var matches []discover.CastDevice
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Name), strings.ToLower(name)) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return discover.CastDevice{}, fmt.Errorf("no Cast device matching %q (run `chorus play` to pick interactively)", name)
	case 1:
		return matches[0], nil
	default:
		return discover.CastDevice{}, fmt.Errorf("%q matches %d Cast devices; be more specific", name, len(matches))
	}
}

func matchAirPlay(devs []output.AirPlayDevice, name string) (output.AirPlayDevice, error) {
	var matches []output.AirPlayDevice
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Name), strings.ToLower(name)) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return output.AirPlayDevice{}, fmt.Errorf("no AirPlay device matching %q (run `chorus play` to pick interactively)", name)
	case 1:
		return matches[0], nil
	default:
		return output.AirPlayDevice{}, fmt.Errorf("%q matches %d AirPlay devices; be more specific", name, len(matches))
	}
}

func matchOutput(outs []output.Device, name string) (output.Device, error) {
	var matches []output.Device
	for _, d := range outs {
		if strings.Contains(strings.ToLower(d.Name), strings.ToLower(name)) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return output.Device{}, fmt.Errorf("no output device matching %q (is it paired? run `chorus play` to pick interactively)", name)
	case 1:
		return matches[0], nil
	default:
		return output.Device{}, fmt.Errorf("%q matches %d output devices; be more specific", name, len(matches))
	}
}

// parseOffsets parses "name=duration" entries into a slice of matchers.
func parseOffsets(entries []string) (map[string]time.Duration, error) {
	m := make(map[string]time.Duration, len(entries))
	for _, e := range entries {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --offset %q (want name=duration, e.g. HW-S700D=2s)", e)
		}
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("invalid --offset %q: %w", e, err)
		}
		m[strings.ToLower(strings.TrimSpace(k))] = d
	}
	return m, nil
}

// offsetFor returns the offset whose key is a substring of the device name.
func offsetFor(m map[string]time.Duration, deviceName string) time.Duration {
	lname := strings.ToLower(deviceName)
	for k, d := range m {
		if strings.Contains(lname, k) {
			return d
		}
	}
	return 0
}
