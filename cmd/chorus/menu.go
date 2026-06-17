package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/wyattjs/chorus/internal/discover"
	"github.com/wyattjs/chorus/internal/output"
)

// menuItem is a single selectable output device in the interactive picker.
type menuItem struct {
	label    string // friendly device name
	status   string // pre-colored badge shown after the label (e.g. connection state)
	detail   string // secondary info shown dimmed (address / transport)
	selected bool

	// Exactly one of these is non-nil, identifying the underlying device.
	cast    *discover.CastDevice
	airplay *output.AirPlayDevice
	bt      *output.BluetoothDevice
}

// menuGroup is a labelled section of devices (Cast / AirPlay / Bluetooth).
type menuGroup struct {
	title string
	color string // ANSI color for the header
	items []*menuItem
}

// ANSI escape helpers.
const (
	ansiReset     = "\033[0m"
	ansiDim       = "\033[2m"
	ansiBold      = "\033[1m"
	ansiBlue      = "\033[34m"
	ansiMagenta   = "\033[35m"
	ansiGreen     = "\033[32m"
	hideCursor    = "\033[?25l"
	showCursor    = "\033[?25h"
	clearToBottom = "\033[J"
)

// btProbeTimeout is the per-device baseband name-request page timeout used to
// decide whether a disconnected paired device is in range. Reachable devices
// answer well within this; absent ones consume it (and serialize), so the scan
// can take roughly this long times the number of absent devices.
const btProbeTimeout = 3 * time.Second

// animateBanner draws the chorus wordmark in a gold→brown gradient, with a purple
// sound wave that radiates outward to its right. The wave keeps pulsing for as
// long as done is open (i.e. while devices are still being discovered), then
// settles into a static cone. Shown when the interactive picker launches (stdout
// is a TTY by the time we get here, so ANSI color is safe).
func animateBanner(done <-chan struct{}) {
	fg := func(n int) string { return fmt.Sprintf("\033[38;5;%dm", n) }
	const bold = "\033[1m"

	// Gold (top) fading to brown (bottom), 256-color palette.
	word := []struct {
		color int
		text  string
	}{
		{220, `      ♪   ♫    ♩    ♬    ♫   ♪`},
		{223, `         _`},
		{222, `     ___| |__   ___  _ __ _   _ ___`},
		{214, `    / __| '_ \ / _ \| '__| | | / __|`},
		{178, `   | (__| | | | (_) | |  | |_| \__ \`},
		{136, `    \___|_| |_|\___/|_|   \__,_|___/`},
	}

	const (
		waveCol = 40 // column the wave starts at, right of the wordmark
		nSlots  = 6  // number of radiating wavefronts
	)
	// Purple shades by distance behind the bright wavefront (0 = brightest).
	purple := []int{177, 141, 134, 98, 61, 60}

	// waveRows renders the 3-row radiating cone for a frame. bright(i) reports the
	// shade and visibility of wavefront slot i; gaps grow outward so the arcs
	// spread apart like an expanding signal.
	waveRows := func(bright func(i int) (shade int, vis bool)) [3]string {
		var rows [3]strings.Builder
		for i := range nSlots {
			gap := strings.Repeat(" ", 1+i)
			shade, vis := bright(i)
			for r := range 3 {
				rows[r].WriteString(gap)
				arc := r == 1 || i >= 1 // middle row always; top/bottom flare outward
				if vis && arc {
					rows[r].WriteString(fg(shade) + bold + ")" + ansiReset)
				} else {
					rows[r].WriteString(" ")
				}
			}
		}
		return [3]string{rows[0].String(), rows[1].String(), rows[2].String()}
	}

	// frame composes the whole banner (wordmark + wave) for one animation step.
	frame := func(bright func(i int) (int, bool)) string {
		wr := waveRows(bright)
		var b strings.Builder
		for li, w := range word {
			line := fg(w.color) + bold + w.text + ansiReset
			var wave string
			switch li { // attach the cone to the three central rows
			case 2:
				wave = wr[0]
			case 3:
				wave = wr[1]
			case 4:
				wave = wr[2]
			}
			if wave != "" {
				pad := max(waveCol-len([]rune(w.text)), 1)
				line += strings.Repeat(" ", pad) + wave
			}
			b.WriteString(line + "\033[K")
			if li < len(word)-1 {
				b.WriteString("\n")
			}
		}
		return b.String()
	}

	// sweep lights slots up to f, brightest at the front (i==f) and fading inward
	// — the bright ring moving outward each frame.
	sweep := func(f int) func(int) (int, bool) {
		return func(i int) (int, bool) {
			if i > f {
				return 0, false
			}
			d := f - i
			if d >= len(purple) {
				d = len(purple) - 1
			}
			return purple[d], true
		}
	}

	// block is the full redrawn region: the banner, a blank spacer line, then a
	// status line. While searching the status shows an animated dot in solid
	// white; once settled it's cleared.
	const white = "\033[97m"
	spin := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	block := func(bright func(int) (int, bool), tick int, settled bool) string {
		body := frame(bright)
		status := ""
		if !settled {
			status = white +
				fmt.Sprintf("   %c  listening for Cast · AirPlay · Bluetooth", spin[tick%len(spin)]) +
				ansiReset
		}
		return body + "\n\n" + status + "\033[K"
	}

	// up moves the cursor from the bottom line of the block back to its top
	// (len(word) wordmark lines + one blank spacer line above the status).
	up := fmt.Sprintf("\r\033[%dA", len(word)+1)

	// reserve keeps a couple of blank lines below the banner so it isn't jammed
	// against the bottom of the terminal while animating: print the blanks once,
	// then step back up onto the status line. They stay put across redraws since
	// each frame only repaints the block above (never the reserved lines).
	const bottomPad = 2
	reserve := strings.Repeat("\n", bottomPad) + fmt.Sprintf("\033[%dA", bottomPad)

	fmt.Print(hideCursor)
	fmt.Print("\n")                       // top margin
	fmt.Print(block(sweep(-1), 0, false)) // wordmark, no wave yet
	fmt.Print(reserve)

	ticker := time.NewTicker(260 * time.Millisecond)
	defer ticker.Stop()
	for tick := 1; ; tick++ {
		select {
		case <-done:
			// Settle into a static cone, fading from bright (near the word) to dim.
			fmt.Print(up)
			fmt.Print(block(func(i int) (int, bool) { return purple[i], true }, 0, true))
			fmt.Print(showCursor + "\n")
			return
		case <-ticker.C:
			fmt.Print(up)
			fmt.Print(block(sweep(tick%nSlots), tick, false))
		}
	}
}

// discoverAll scans for Cast, AirPlay, and audio-output devices concurrently and
// returns them grouped for the picker. Per-category errors are tolerated so one
// failing transport doesn't hide the others.
func discoverAll(ctx context.Context, wait time.Duration) []menuGroup {
	var (
		wg    sync.WaitGroup
		casts []discover.CastDevice
		airs  []output.AirPlayDevice
		bts   []output.BluetoothDevice
	)

	wg.Add(3)
	go func() { defer wg.Done(); casts, _ = discover.BrowseCast(ctx, wait) }()
	go func() { defer wg.Done(); airs, _ = output.ListAirPlayDevices(ctx) }()
	go func() { defer wg.Done(); bts, _ = output.ListBluetoothDevices(ctx, btProbeTimeout) }()
	wg.Wait()

	castGroup := menuGroup{title: "Google Cast", color: ansiBlue}
	for i := range casts {
		c := casts[i]
		castGroup.items = append(castGroup.items, &menuItem{
			label:  c.Name,
			detail: fmt.Sprintf("%s:%d", c.Host, c.Port),
			cast:   &c,
		})
	}

	airGroup := menuGroup{title: "AirPlay", color: ansiMagenta}
	for i := range airs {
		a := airs[i]
		airGroup.items = append(airGroup.items, &menuItem{
			label:   a.Name,
			detail:  fmt.Sprintf("%s · %s", a.Addr, a.Proto),
			airplay: &a,
		})
	}

	btGroup := menuGroup{title: "Bluetooth", color: ansiGreen}
	for i := range bts {
		d := bts[i]
		status := ansiDim + "○ in range" + ansiReset
		if d.Connected {
			status = ansiGreen + "● connected" + ansiReset
		}
		btGroup.items = append(btGroup.items, &menuItem{
			label:  d.Name,
			status: status,
			detail: d.Address,
			bt:     &d,
		})
	}

	return []menuGroup{castGroup, airGroup, btGroup}
}

// selectDevices runs the interactive multi-select picker over the given groups
// and returns the items the user chose. It returns nil if the user cancels.
func selectDevices(groups []menuGroup) ([]*menuItem, error) {
	// Flatten selectable items for cursor navigation.
	var flat []*menuItem
	for _, g := range groups {
		flat = append(flat, g.items...)
	}
	if len(flat) == 0 {
		return nil, fmt.Errorf("no Cast, AirPlay, or Bluetooth output devices found")
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("interactive picker needs a terminal: %w", err)
	}
	defer term.Restore(fd, oldState)

	fmt.Print(hideCursor)
	defer fmt.Print(showCursor)

	cursor := 0
	prevLines := 0
	render := func() {
		if prevLines > 0 {
			fmt.Printf("\033[%dA", prevLines) // move cursor up to top of menu
		}
		fmt.Print("\r" + clearToBottom)
		prevLines = renderMenu(groups, flat, cursor)
	}
	render()

	in := make([]byte, 8)
	for {
		n, err := os.Stdin.Read(in)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("input closed")
		}
		switch {
		case in[0] == 3 || in[0] == 'q' || (n == 1 && in[0] == 27): // ctrl-c, q, esc
			fmt.Print("\r\n")
			return nil, nil
		case in[0] == '\r' || in[0] == '\n': // enter -> confirm
			var chosen []*menuItem
			for _, it := range flat {
				if it.selected {
					chosen = append(chosen, it)
				}
			}
			fmt.Print("\r\n")
			return chosen, nil
		case in[0] == ' ': // space -> toggle
			flat[cursor].selected = !flat[cursor].selected
			render()
		case n >= 3 && in[0] == 27 && in[1] == '[': // arrow keys
			switch in[2] {
			case 'A': // up
				if cursor > 0 {
					cursor--
				}
			case 'B': // down
				if cursor < len(flat)-1 {
					cursor++
				}
			}
			render()
		case in[0] == 'k':
			if cursor > 0 {
				cursor--
			}
			render()
		case in[0] == 'j':
			if cursor < len(flat)-1 {
				cursor++
			}
			render()
		}
	}
}

// startSpinner shows an animated spinner with the given label on stderr until
// the returned stop function is called. stop(done) clears the spinner line and,
// if done is non-empty, prints it as the final status line.
func startSpinner(label string) (stop func(done string)) {
	stopCh := make(chan string)
	finished := make(chan struct{})
	go func() {
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case done := <-stopCh:
				fmt.Fprint(os.Stderr, "\r\033[K") // clear the spinner line
				if done != "" {
					fmt.Fprintln(os.Stderr, done)
				}
				close(finished)
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r\033[K%c %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return func(done string) {
		stopCh <- done
		<-finished
	}
}

// connectBluetooth ensures a chosen Bluetooth device is connected and resolves it
// to a renderable CoreAudio output, showing a spinner throughout. A freshly
// connected device can take a moment to appear as an audio output, so it polls.
func connectBluetooth(ctx context.Context, btd output.BluetoothDevice) (output.Device, error) {
	if dev, ok, err := output.ResolveOutputByName(ctx, btd.Name); err == nil && ok && btd.Connected {
		return dev, nil
	}

	stop := startSpinner(fmt.Sprintf("Connecting to %s…", btd.Name))
	if !btd.Connected {
		if err := output.ConnectBluetooth(ctx, btd.Address); err != nil {
			stop(ansiDim + "✗ " + btd.Name + ansiReset)
			return output.Device{}, fmt.Errorf("connecting to %s: %w", btd.Name, err)
		}
	}

	// Wait for the connected device to show up as a CoreAudio output.
	for range 20 {
		if dev, ok, err := output.ResolveOutputByName(ctx, btd.Name); err == nil && ok {
			stop(ansiGreen + "● " + ansiReset + "Connected to " + btd.Name)
			return dev, nil
		}
		select {
		case <-ctx.Done():
			stop("")
			return output.Device{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	stop(ansiDim + "✗ " + btd.Name + ansiReset)
	return output.Device{}, fmt.Errorf("%s connected but did not appear as a CoreAudio output", btd.Name)
}

// renderMenu prints the grouped device list and returns the number of lines drawn.
func renderMenu(groups []menuGroup, flat []*menuItem, cursor int) int {
	var b strings.Builder
	lines := 0
	writeln := func(s string) { b.WriteString(s + "\r\n"); lines++ }

	writeln(ansiBold + "Select output devices" + ansiReset +
		ansiDim + "  (↑/↓ move · space toggle · enter confirm · q cancel)" + ansiReset)

	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		writeln("")
		writeln("  " + g.color + ansiBold + g.title + ansiReset)
		for _, it := range g.items {
			pointer := "  "
			if flat[cursor] == it {
				pointer = g.color + "❯ " + ansiReset
			}
			box := "[ ]"
			if it.selected {
				box = g.color + "[✓]" + ansiReset
			}
			line := fmt.Sprintf("  %s%s %s", pointer, box, it.label)
			if it.status != "" {
				line += "  " + it.status
			}
			if it.detail != "" {
				line += "  " + ansiDim + it.detail + ansiReset
			}
			writeln(line)
		}
	}

	fmt.Print(b.String())
	return lines
}
