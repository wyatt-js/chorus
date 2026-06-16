// Package pipeline wires system-audio capture to one or more time-offset outputs.
package pipeline

import (
	"context"
	"errors"
	"time"

	"github.com/wyattjs/airtooth-sync/internal/audio"
	"github.com/wyattjs/airtooth-sync/internal/capture"
	"github.com/wyattjs/airtooth-sync/internal/output"
)

// Target is an output plus the delay applied to its stream.
type Target struct {
	Output output.Output
	Offset time.Duration
}

// Options configures a play session.
type Options struct {
	Targets []Target
}

// Run captures system audio and fans it out to every target until ctx is
// cancelled or capture ends.
func Run(ctx context.Context, opts Options) error {
	if len(opts.Targets) == 0 {
		return errors.New("no outputs selected")
	}

	b := output.New(audio.StereoCD)
	for _, t := range opts.Targets {
		b.Add(t.Output, t.Offset)
	}

	// Start local-rendering outputs first so we can exclude their processes from
	// the capture tap — otherwise their output feeds back into the capture.
	excludePIDs, err := b.Prestart(ctx)
	if err != nil {
		return err
	}

	cap, err := capture.Start(ctx, excludePIDs)
	if err != nil {
		return err
	}
	// Kill audiotee as soon as we're cancelled so the read loop unblocks, and
	// again on return to guarantee no orphaned sidecar.
	go func() { <-ctx.Done(); cap.Stop() }()
	defer func() { cap.Stop(); _ = cap.Wait() }()

	return b.Run(ctx, cap.PCM)
}
