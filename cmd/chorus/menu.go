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
	detail   string // secondary info shown dimmed (address / transport)
	selected bool

	// Exactly one of these is non-nil, identifying the underlying device.
	cast    *discover.CastDevice
	airplay *output.AirPlayDevice
	bt      *output.Device
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

// discoverAll scans for Cast, AirPlay, and audio-output devices concurrently and
// returns them grouped for the picker. Per-category errors are tolerated so one
// failing transport doesn't hide the others.
func discoverAll(ctx context.Context, wait time.Duration) []menuGroup {
	var (
		wg    sync.WaitGroup
		casts []discover.CastDevice
		airs  []output.AirPlayDevice
		outs  []output.Device
	)

	wg.Add(3)
	go func() { defer wg.Done(); casts, _ = discover.BrowseCast(ctx, wait) }()
	go func() { defer wg.Done(); airs, _ = output.ListAirPlayDevices(ctx) }()
	go func() { defer wg.Done(); outs, _ = output.ListOutputDevices(ctx) }()
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

	btGroup := menuGroup{title: "Bluetooth / Audio output", color: ansiGreen}
	for i := range outs {
		d := outs[i]
		// Skip the built-in output; it would just feed audio back into the tap.
		if d.Transport == "builtin" {
			continue
		}
		btGroup.items = append(btGroup.items, &menuItem{
			label:  d.Name,
			detail: d.Transport,
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
			if it.detail != "" {
				line += "  " + ansiDim + it.detail + ansiReset
			}
			writeln(line)
		}
	}

	fmt.Print(b.String())
	return lines
}
