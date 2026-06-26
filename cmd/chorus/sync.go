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
// test chirp on it; chorus measures that device's latency by ear of the mic and
// re-aligns the measured devices immediately (delaying the faster ones to match
// the slowest). Once every device is measured the alignment is complete, so the
// screen closes itself.
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
			fmt.Print("\r\n  " + ansiDim + "sync closed" + ansiReset + "\r\n")
			return
		case c == 'r': // start over: clear measurements and remove all delay
			for _, name := range names {
				sess.SetOffset(name, 0)
			}
			base = map[string]time.Duration{}
		case c >= '1' && c <= '9':
			idx := int(c - '1')
			if idx >= len(names) {
				continue
			}
			measureOne(ctx, sess, names[idx], base)
			if len(base) == len(names) { // all devices done → aligned, close
				fmt.Printf("\r\n  %s✓ aligned %d devices%s\r\n", ansiGreen, len(names), ansiReset)
				time.Sleep(800 * time.Millisecond)
				return
			}
		}
	}
}

// measureOne plays one chirp on a device, records its base latency, and re-aligns
// every measured device. Measure blocks for several seconds (the chirp can take
// that long to reach a Cast device's buffer), so a spinner runs meanwhile.
func measureOne(ctx context.Context, sess *pipeline.Session, name string, base map[string]time.Duration) {
	fmt.Print("\r\n") // blank line between the key hints and the result/spinner
	stop := startSpinner("playing test tone on " + name + " — keep the Mac close…")

	// Subtract any offset already applied so we store the device's own latency,
	// which keeps the live re-alignment below from compounding across measurements.
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
	alignOffsets(sess, base)
	stop(fmt.Sprintf("%s●%s %s — %d ms", ansiGreen, ansiReset, name, b.Milliseconds()))
	time.Sleep(300 * time.Millisecond)
}

// alignOffsets delays every measured device to match the slowest one. Offsets are
// absolute, so re-running it after each measurement converges without compounding.
func alignOffsets(sess *pipeline.Session, base map[string]time.Duration) {
	var maxLat time.Duration
	for _, l := range base {
		if l > maxLat {
			maxLat = l
		}
	}
	for n, b := range base {
		sess.SetOffset(n, maxLat-b)
	}
}

// drawSync prints the calibration screen: each device with its measured latency
// or "not measured", plus the key hints.
func drawSync(names []string, base map[string]time.Duration) {
	var b strings.Builder
	b.WriteString("\r\n  " + ansiBold + "sync" + ansiReset + ansiDim + " — measure each speaker; they align automatically" + ansiReset + "\r\n\r\n")
	for i, name := range names {
		status := ansiDim + "not measured" + ansiReset
		if lat, ok := base[name]; ok {
			status = ansiGreen + fmt.Sprintf("%d ms", lat.Milliseconds()) + ansiReset
		}
		fmt.Fprintf(&b, "   %s%d%s. %-22s %s\r\n", ansiBold, i+1, ansiReset, name, status)
	}
	b.WriteString("\r\n  " + ansiDim + "walk the Mac near a device, then press its number to play a test tone" + ansiReset + "\r\n")
	b.WriteString("   " + ansiBold + "[1-9]" + ansiReset + " measure   " +
		ansiBold + "[r]" + ansiReset + " reset   " +
		ansiBold + "[q]" + ansiReset + " close\r\n")
	fmt.Print(b.String())
}
