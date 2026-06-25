package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
	"github.com/wyattjs/chorus/internal/calibrate"
	"github.com/wyattjs/chorus/internal/pipeline"
)

// runSync drives acoustic calibration from the in-session control loop (terminal
// already in raw mode). The user carries the Mac near each device and plays a
// test chirp on it; chorus measures that device's latency by ear of the mic.
// Once two or more are measured, it aligns them by delaying the faster ones to
// match the slowest (offset_i = max base latency − base latency_i).
func runSync(ctx context.Context, sess *pipeline.Session, active map[string]activeEntry) {
	names := make([]string, 0, len(active))
	for _, e := range active {
		names = append(names, e.name)
	}
	sort.Strings(names)

	// Base (offset-free) latency per measured device.
	base := map[string]time.Duration{}

	for {
		drawSync(names, base)
		buf := make([]byte, 8)
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return
		}
		switch c := buf[0]; {
		case c == 'q' || c == 3 || c == 27: // q, Ctrl-C, Esc
			fmt.Print("\r\n  " + ansiDim + "sync cancelled (offsets unchanged)" + ansiReset + "\r\n")
			return
		case c == 'r':
			base = map[string]time.Duration{}
		case c == 'a':
			if len(base) < 2 {
				fmt.Print("\r\n  " + ansiDim + "measure at least two devices before aligning" + ansiReset + "\r\n")
				time.Sleep(900 * time.Millisecond)
				continue
			}
			applyOffsets(sess, base)
			return
		case c >= '1' && c <= '9':
			if idx := int(c - '1'); idx < len(names) {
				measureOne(ctx, sess, names[idx], base)
			}
		}
	}
}

// measureOne plays one chirp on a device and records its base latency. Measure
// blocks for several seconds (the chirp can take that long to reach a Cast
// device's buffer), so a spinner runs meanwhile.
func measureOne(ctx context.Context, sess *pipeline.Session, name string, base map[string]time.Duration) {
	stop := startSpinner("playing test tone on " + name + " — keep the Mac close…")

	// Subtract any offset already applied so we store the device's own latency,
	// which keeps re-runs (measure → align → measure again) from compounding.
	cur := sess.Offset(name)
	lat, err := calibrate.Measure(ctx, sess, name, audio.StereoCD)
	if err != nil {
		stop(ansiDim + "✗ " + err.Error() + ansiReset)
		time.Sleep(400 * time.Millisecond)
		return
	}
	b := lat - cur
	if b < 0 {
		b = 0
	}
	base[name] = b
	stop(fmt.Sprintf("%s●%s %s — %d ms", ansiGreen, ansiReset, name, b.Milliseconds()))
	time.Sleep(300 * time.Millisecond)
}

// applyOffsets delays every measured device to match the slowest one.
func applyOffsets(sess *pipeline.Session, base map[string]time.Duration) {
	var maxLat time.Duration
	for _, l := range base {
		if l > maxLat {
			maxLat = l
		}
	}
	names := make([]string, 0, len(base))
	for n := range base {
		names = append(names, n)
	}
	sort.Strings(names)

	fmt.Print("\r\n  " + ansiGreen + "aligned to the slowest device:" + ansiReset + "\r\n")
	for _, n := range names {
		off := maxLat - base[n]
		sess.SetOffset(n, off)
		fmt.Printf("   %-22s +%d ms\r\n", n, off.Milliseconds())
	}
	time.Sleep(900 * time.Millisecond)
}

// drawSync prints the calibration screen: each device with its measured latency
// or "not measured", plus the key hints.
func drawSync(names []string, base map[string]time.Duration) {
	var b strings.Builder
	b.WriteString("\r\n  " + ansiBold + "sync" + ansiReset + ansiDim + " — measure each speaker's delay, then align them" + ansiReset + "\r\n\r\n")
	for i, name := range names {
		status := ansiDim + "not measured" + ansiReset
		if lat, ok := base[name]; ok {
			status = ansiGreen + fmt.Sprintf("%d ms", lat.Milliseconds()) + ansiReset
		}
		fmt.Fprintf(&b, "   %s%d%s. %-22s %s\r\n", ansiBold, i+1, ansiReset, name, status)
	}
	b.WriteString("\r\n  " + ansiDim + "walk the Mac near a device, then press its number to play a test tone" + ansiReset + "\r\n")
	b.WriteString("   " + ansiBold + "[1-9]" + ansiReset + " measure   " +
		ansiBold + "[a]" + ansiReset + " apply & align   " +
		ansiBold + "[r]" + ansiReset + " reset   " +
		ansiBold + "[q]" + ansiReset + " back\r\n")
	fmt.Print(b.String())
}
