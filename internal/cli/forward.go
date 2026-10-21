package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/agent"
	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
	"github.com/DrDowellsHead/Aegis/internal/spool"
	"github.com/DrDowellsHead/Aegis/internal/telemetry"
)

type secureSender struct {
	client  *telemetry.Client
	agentID string
}

func (s secureSender) Send(ctx context.Context, r spool.Record) error {
	if r.AgentID != s.agentID {
		return fmt.Errorf("queued record belongs to another agent")
	}
	return s.client.Send(ctx, r.MeasurementID, r.Snapshot)
}

func runForward(ctx context.Context, args []string, errOut io.Writer) int {
	flags := flag.NewFlagSet("forward", flag.ContinueOnError)
	flags.SetOutput(errOut)
	address := flags.String("server", "127.0.0.1:9090", "TLS gRPC server address")
	credentialsPath := flags.String("credentials", "", "private agent credentials file")
	caPath := flags.String("ca-cert", "", "trusted CA certificate path")
	disk := flags.String("disk", ".", "filesystem path to observe")
	queueDir := flags.String("queue-dir", "", "persistent queue directory (default: user config directory)")
	interval := flags.Duration("interval", 5*time.Second, "pause between measurements (>=100ms)")
	count := flags.Int("count", 0, "number of collections (0 = until Ctrl+C)")
	maxMB := flags.Int("max-queue-mb", 64, "queue capacity in MiB (1..1024)")
	requestTimeout := flags.Duration("request-timeout", 5*time.Second, "per-RPC timeout")
	heartbeatInterval := flags.Duration("heartbeat-interval", 15*time.Second, "authenticated liveness check interval (1s..1h; 0 disables)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *caPath == "" || *maxMB < 1 || *maxMB > 1024 ||
		*count < 0 || *interval < 100*time.Millisecond || *interval > 24*time.Hour ||
		*requestTimeout <= 0 || *requestTimeout > time.Minute ||
		(*heartbeatInterval != 0 && (*heartbeatInterval < time.Second || *heartbeatInterval > time.Hour)) {
		fmt.Fprintln(errOut, "invalid arguments: --ca-cert required; max-queue-mb 1..1024; interval 100ms..24h; request-timeout 0..1m; heartbeat-interval 0 or 1s..1h; count >=0")
		return 2
	}
	if *credentialsPath == "" {
		var err error
		*credentialsPath, err = identity.DefaultPath()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	creds, err := identity.LoadCredentials(*credentialsPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if *queueDir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		*queueDir = filepath.Join(root, "aegis", "spool", creds.AgentID)
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
	queue, err := spool.Open(*queueDir, creds.AgentID, int64(*maxMB)<<20)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer queue.Close() // always release the process lock
	client, err := telemetry.NewClient(*address, creds, *caPath)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	defer client.Close()
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	n, bytes := queue.Stats()
	logger.Info("forwarder started; records removed only on durable ACK",
		"agent_id", creds.AgentID, "queue_entries", n, "queue_bytes", bytes,
		"queue_dir", *queueDir, "server", *address)
	// A separate heartbeat loop keeps the agent reachable even while the
	// collector or spool sender is blocked. Both loops join on shutdown.
	var heartbeatDone chan struct{}
	var stopHeartbeat context.CancelFunc
	if *heartbeatInterval > 0 {
		var heartbeatCtx context.Context
		heartbeatCtx, stopHeartbeat = context.WithCancel(ctx)
		heartbeatDone = make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			_ = agent.RunHeartbeat(heartbeatCtx, client, *heartbeatInterval, *requestTimeout, logger)
		}()
	}
	err = agent.Forward(ctx, collector, secureSender{client: client, agentID: creds.AgentID},
		queue, logger, agent.ForwardOptions{
			Interval: *interval, Count: *count, RequestTimeout: *requestTimeout,
			RetryMin: time.Second, RetryMax: 30 * time.Second,
		})
	if stopHeartbeat != nil {
		stopHeartbeat()
		<-heartbeatDone // no heartbeat goroutine survives Forward
	}
	if err != nil {
		logger.Error("forwarding stopped with error", "error", err)
		return 1
	}
	n, bytes = queue.Stats()
	logger.Info("forwarder stopped; remaining records have no durable ACK",
		"queue_entries", n, "queue_bytes", bytes)
	return 0
}
