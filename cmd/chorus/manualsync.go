package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/wyattjs/chorus/internal/output"
	"github.com/wyattjs/chorus/internal/pipeline"
)

// syncStepFine and syncStepCoarse are the per-keypress trim increments. Terminal
// key-repeat (holding the key) makes the fine step fast enough for big moves,
// while the coarse step jumps quickly toward the multi-second buffers of Cast and
// AirPlay.
const (
	syncStepFine   = 10 * time.Millisecond
	syncStepCoarse = 250 * time.Millisecond
	syncBarHalf    = 12              // half-width of the centered bar, in cells
	syncBarFloor   = 1 * time.Second // bar full-scale floor; grows past it as needed
)

// runManualSync is the manual delay screen (the `d` key). Each device has a
// signed trim the user dials with the arrow keys: right delays it, left advances
// it relative to the others. Only relative timing matters, so the trims are
// normalized to non-negative output delays (the earliest device gets 0, the rest
// are delayed to match) and applied live — no playback gap — so the user aligns
// by ear. The spread is capped at output.MaxOffset (the per-sink buffer depth).
// Terminal is already in raw mode (controlLoop).
func runManualSync(sess *pipeline.Session, active map[string]activeEntry) {
	names := make([]string, 0, len(active))
	for _, e := range active {
		names = append(names, e.name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return
	}

	// Seed the signed trims from the current output delays.
	trim := make(map[string]time.Duration, len(names))
	for _, n := range names {
		trim[n] = sess.Offset(n)
	}

	// apply pushes the normalized (non-negative) delays to the outputs.
	apply := func() {
		min := trim[names[0]]
		for _, n := range names {
			if trim[n] < min {
				min = trim[n]
			}
		}
		for _, n := range names {
			sess.SetOffset(n, trim[n]-min)
		}
	}
	spread := func() time.Duration {
		lo, hi := trim[names[0]], trim[names[0]]
		for _, n := range names {
			if trim[n] < lo {
				lo = trim[n]
			}
			if trim[n] > hi {
				hi = trim[n]
			}
		}
		return hi - lo
	}

	cursor := 0
	adjust := func(d time.Duration) {
		n := names[cursor]
		old := trim[n]
		trim[n] = old + d
		if spread() > output.MaxOffset { // would exceed the buffer depth; reject
			trim[n] = old
			return
		}
		apply()
	}

	fmt.Print(hideCursor)
	defer fmt.Print(showCursor)
	prevLines := 0
	render := func() {
		if prevLines > 0 {
			fmt.Printf("\033[%dA", prevLines)
		}
		fmt.Print("\r" + clearToBottom)
		prevLines = renderManualSync(names, trim, cursor)
	}
	render()

	in := make([]byte, 8)
	for {
		n, err := os.Stdin.Read(in)
		if err != nil || n == 0 {
			return
		}
		switch {
		case in[0] == 'q' || in[0] == 3 || (n == 1 && in[0] == 27): // q, ctrl-c, esc
			fmt.Print("\r\n")
			return
		case n >= 3 && in[0] == 27 && in[1] == '[': // arrow keys
			switch in[2] {
			case 'A': // up
				if cursor > 0 {
					cursor--
				}
			case 'B': // down
				if cursor < len(names)-1 {
					cursor++
				}
			case 'C': // right: later
				adjust(syncStepFine)
			case 'D': // left: earlier
				adjust(-syncStepFine)
			}
		case in[0] == 'k':
			if cursor > 0 {
				cursor--
			}
		case in[0] == 'j':
			if cursor < len(names)-1 {
				cursor++
			}
		case in[0] == 'l':
			adjust(syncStepFine)
		case in[0] == 'h':
			adjust(-syncStepFine)
		case in[0] == ']':
			adjust(syncStepCoarse)
		case in[0] == '[':
			adjust(-syncStepCoarse)
		case in[0] == '0': // recenter the selected device
			trim[names[cursor]] = 0
			apply()
		}
		render()
	}
}

// renderManualSync draws the centered slider list and returns the number of lines
// printed so the caller can move the cursor back up on the next redraw.
func renderManualSync(names []string, trim map[string]time.Duration, cursor int) int {
	// Bar full-scale: the larger of the floor and the biggest absolute trim.
	scale := syncBarFloor
	width := 0
	for _, n := range names {
		if a := abs(trim[n]); a > scale {
			scale = a
		}
		if l := len(n); l > width {
			width = l
		}
	}

	var b strings.Builder
	lines := 0
	writeln := func(s string) { b.WriteString(s + "\r\n"); lines++ }

	writeln("")
	writeln(ansiBold + "delays" + ansiReset + ansiDim + " — trim each device: ← earlier · → later (relative)" + ansiReset)
	writeln("")
	for i, n := range names {
		pointer := "  "
		if i == cursor {
			pointer = ansiGreen + "❯ " + ansiReset
		}
		writeln(fmt.Sprintf("  %s%-*s  %s  %s%+5d ms%s",
			pointer, width, n, centeredBar(trim[n], scale), ansiBold, trim[n].Milliseconds(), ansiReset))
	}
	writeln("")
	writeln("  " + ansiDim + "↑/↓ select · ←/→ ±10ms · [ / ] ±250ms · 0 center · q done  (±10s)" + ansiReset)
	fmt.Print(b.String())
	return lines
}

// centeredBar draws a bar with a midpoint: filled left of center for a negative
// (earlier) trim, right for a positive (later) one, scaled so |trim|==scale fills
// half the width.
func centeredBar(v, scale time.Duration) string {
	cells := make([]rune, 2*syncBarHalf+1)
	for i := range cells {
		cells[i] = '·'
	}
	cells[syncBarHalf] = '│' // center mark
	color := ansiGreen
	if v < 0 {
		color = ansiMagenta
	}
	n := 0
	if scale > 0 {
		n = int(abs(v) * syncBarHalf / scale)
	}
	if n > syncBarHalf {
		n = syncBarHalf
	}
	for i := 1; i <= n; i++ {
		if v >= 0 {
			cells[syncBarHalf+i] = '█'
		} else {
			cells[syncBarHalf-i] = '█'
		}
	}
	var sb strings.Builder
	for _, r := range cells {
		if r == '█' {
			sb.WriteString(color + string(r) + ansiReset)
		} else {
			sb.WriteString(ansiDim + string(r) + ansiReset)
		}
	}
	return sb.String()
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
