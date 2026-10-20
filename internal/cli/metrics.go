package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/agent"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

func runMetrics(ctx context.Context, command string, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("disk", ".", "path on the filesystem to measure")
	window := flags.Duration("cpu-window", 250*time.Millisecond, "CPU observation window (100ms to 10s)")
	timeout := flags.Duration("timeout", 3*time.Second, "cooperative collection deadline (maximum 1m)")
	interval := 5 * time.Second
	count := 1
	if command == "agent" {
		count = 0
		flags.DurationVar(&interval, "interval", 5*time.Second, "pause after each sample (100ms to 24h)")
		flags.IntVar(&count, "count", 0, "number of samples; 0 runs until Ctrl+C")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errOut, "unexpected positional arguments")
		return 2
	}
	if *timeout <= *window || interval < 100*time.Millisecond || interval > 24*time.Hour || count < 0 {
		fmt.Fprintln(errOut, "timeout must exceed CPU window; interval must be 100ms..24h; count must be non-negative")
		return 2
	}
	source, err := metrics.NewSystem(*path, *window)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	collector, err := metrics.New(source, *timeout)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	err = agent.Run(ctx, collector, out, interval, count)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 0
	}
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
