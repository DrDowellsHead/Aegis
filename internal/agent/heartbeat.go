package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Heartbeater is defined by the consumer and makes no assumptions about the
// transport. Heartbeat success must mean an authenticated committed receipt.
type Heartbeater interface {
	Heartbeat(context.Context) error
}

// RunHeartbeat runs independently of metrics sampling, spool replay and
// retries. It sends immediately and then periodically. Failed heartbeats
// are logged; telemetry forwarding must remain able to recover and replay.
func RunHeartbeat(ctx context.Context, sender Heartbeater, interval, timeout time.Duration, logger *slog.Logger) error {
	if sender == nil || logger == nil || interval < time.Second ||
		interval > time.Hour || timeout <= 0 || timeout > time.Minute {
		return fmt.Errorf("invalid heartbeat dependencies or interval/timeout")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		err := sender.Heartbeat(callCtx)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			logger.Warn("heartbeat not acknowledged; telemetry spool unaffected", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
