// Command chorus captures macOS system audio and relays it to AirPlay/Bluetooth
// speakers, time-aligned. Phase 0 supports a single AirPlay output.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:           "chorus",
		Short:         "Synchronized multi-device audio relay",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(devicesCmd(), playCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
