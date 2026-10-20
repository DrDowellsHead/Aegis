package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
	"github.com/DrDowellsHead/Aegis/internal/telemetry"
)

func runReceive(ctx context.Context, args []string, errOut io.Writer) int {
	flags := flag.NewFlagSet("receive", flag.ContinueOnError)
	flags.SetOutput(errOut)
	address := flags.String("listen", "127.0.0.1:9090", "local gRPC address (loopback only)")
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
	if err := telemetry.RequireLoopback(*address); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	if err := telemetry.ServeReceiver(ctx, listener, logger); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func runSend(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("send", flag.ContinueOnError)
	flags.SetOutput(errOut)
	address := flags.String("server", "127.0.0.1:9090", "local gRPC receiver (loopback only)")
	agentID := flags.String("agent-id", "local-demo", "temporary unverified agent label")
	disk := flags.String("disk", ".", "path on the filesystem to measure")
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
	if err := telemetry.RequireLoopback(*address); err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if len(*agentID) == 0 || len(*agentID) > 128 {
		fmt.Fprintln(errOut, "agent-id must be 1..128 characters")
		return 2
	}
	system, err := metrics.NewSystem(*disk, 250*time.Millisecond)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	collector, err := metrics.New(system, 3*time.Second)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	snapshot, err := collector.Collect(ctx)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	id, err := telemetry.SendOnce(ctx, *address, *agentID, snapshot)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(map[string]any{
		"received": true, "durable": false, "measurement_id": id,
	}); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}
