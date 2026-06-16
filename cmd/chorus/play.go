package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
				targets, err = pickTargets(ctx, wait, volume, pin, offMap)
				if err != nil {
					return err
				}
				if len(targets) == 0 {
					return fmt.Errorf("no devices selected")
				}
				return pipeline.Run(ctx, pipeline.Options{Targets: targets})
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

// pickTargets scans for all devices, runs the interactive picker, and converts
// the user's selection into pipeline targets.
func pickTargets(ctx context.Context, wait time.Duration, volume int, pin string, offMap map[string]time.Duration) ([]pipeline.Target, error) {
	fmt.Fprintln(os.Stderr, "Scanning for Cast, AirPlay, and Bluetooth devices…")
	groups := discoverAll(ctx, wait)

	chosen, err := selectDevices(groups)
	if err != nil {
		return nil, err
	}

	var targets []pipeline.Target
	for _, it := range chosen {
		switch {
		case it.cast != nil:
			targets = append(targets, castTarget(*it.cast, volume, offMap))
		case it.airplay != nil:
			targets = append(targets, airplayTarget(*it.airplay, pin, offMap))
		case it.bt != nil:
			targets = append(targets, btTarget(*it.bt, offMap))
		}
	}
	return targets, nil
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
