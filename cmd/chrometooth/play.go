package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wyattjs/airtooth-sync/internal/audio"
	"github.com/wyattjs/airtooth-sync/internal/discover"
	"github.com/wyattjs/airtooth-sync/internal/output"
	"github.com/wyattjs/airtooth-sync/internal/pipeline"
)

func playCmd() *cobra.Command {
	var (
		casts   []string
		bts     []string
		offsets []string
		volume  int
		wait    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "play",
		Short: "Capture system audio and stream it to Cast and/or Bluetooth outputs",
		Long: "Capture system audio and fan it out to one or more outputs.\n\n" +
			"Examples:\n" +
			"  airtooth play --cast \"The Frame\"\n" +
			"  airtooth play --cast \"The Frame\" --bt \"HW-S700D\"\n" +
			"  airtooth play --cast \"The Frame\" --bt \"HW-S700D\" --offset HW-S700D=2s",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if len(casts) == 0 && len(bts) == 0 {
				return fmt.Errorf("select at least one output with --cast and/or --bt (see `airtooth devices`)")
			}

			offMap, err := parseOffsets(offsets)
			if err != nil {
				return err
			}

			var targets []pipeline.Target

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
					targets = append(targets, pipeline.Target{
						Output: output.NewCast(dev, audio.StereoCD, volume),
						Offset: offsetFor(offMap, dev.Name),
					})
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
					targets = append(targets, pipeline.Target{
						Output: output.NewBT(dev),
						Offset: offsetFor(offMap, dev.Name),
					})
				}
			}

			return pipeline.Run(ctx, pipeline.Options{Targets: targets})
		},
	}

	cmd.Flags().StringArrayVar(&casts, "cast", nil, "Cast device name (substring); repeatable")
	cmd.Flags().StringArrayVar(&bts, "bt", nil, "Bluetooth/output device name (substring); repeatable")
	cmd.Flags().StringArrayVar(&offsets, "offset", nil, "per-device delay, e.g. --offset HW-S700D=2s; repeatable")
	cmd.Flags().IntVar(&volume, "volume", -1, "Cast volume 0-100 (default: leave device unchanged)")
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Second, "how long to look for Cast devices")
	return cmd
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
		return discover.CastDevice{}, fmt.Errorf("no Cast device matching %q (try `airtooth devices`)", name)
	case 1:
		return matches[0], nil
	default:
		return discover.CastDevice{}, fmt.Errorf("%q matches %d Cast devices; be more specific", name, len(matches))
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
		return output.Device{}, fmt.Errorf("no output device matching %q (is it paired? try `airtooth devices`)", name)
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
