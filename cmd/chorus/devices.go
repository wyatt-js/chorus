package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/wyattjs/chorus/internal/discover"
	"github.com/wyattjs/chorus/internal/output"
)

func devicesCmd() *cobra.Command {
	var wait time.Duration

	cmd := &cobra.Command{
		Use:   "devices",
		Short: "List Google Cast, AirPlay, and audio output (Bluetooth) devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			casts, err := discover.BrowseCast(ctx, wait)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(os.Stdout, "Google Cast:")
			if len(casts) == 0 {
				fmt.Fprintln(os.Stdout, "  (none found)")
			} else {
				fmt.Fprintln(tw, "  NAME\tADDRESS\tPORT")
				for _, c := range casts {
					fmt.Fprintf(tw, "  %s\t%s\t%d\n", c.Name, c.Host, c.Port)
				}
				tw.Flush()
			}

			airs, err := output.ListAirPlayDevices(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "\nAirPlay receivers:")
			if len(airs) == 0 {
				fmt.Fprintln(os.Stdout, "  (none found)")
			} else {
				tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "  NAME\tADDRESS\tPROTOCOL\tID")
				for _, a := range airs {
					fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", a.Name, a.Addr, a.Proto, a.ID)
				}
				tw.Flush()
			}

			outs, err := output.ListOutputDevices(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stdout, "\nAudio output devices (Bluetooth = soundbar/speaker):")
			tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "  NAME\tTRANSPORT\tUID")
			for _, d := range outs {
				fmt.Fprintf(tw, "  %s\t%s\t%s\n", d.Name, d.Transport, d.UID)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 3*time.Second, "how long to listen for Cast devices")
	return cmd
}
