package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
	"github.com/DrDowellsHead/Aegis/internal/watch"
)

// runWatch is an optional supervised process. It never sends messages:
// incidentEvent commits outbox jobs; "aegis notify" delivers them.
func runWatch(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	flags.SetOutput(errOut)
	interval := flags.Duration("interval", 30*time.Second,
		"delay between completed fleet scans (10s to 1h)")
	offlineAfter := flags.Duration("offline-after", time.Minute,
		"agent contact timeout (10s to 24h)")
	once := flags.Bool("once", false, "run one fleet scan then exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 ||
		*interval < 10*time.Second || *interval > time.Hour ||
		*offlineAfter < 10*time.Second || *offlineAfter > 24*time.Hour ||
		*interval%time.Second != 0 || *offlineAfter%time.Second != 0 {
		fmt.Fprintln(errOut, "invalid watch arguments (interval 10s..1h, offline-after 10s..24h)")
		return 2
	}

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	db, err := openConfiguredDatabase(checkCtx)
	if err != nil {
		cancel()
		fmt.Fprintln(errOut, "watch database unavailable")
		return 1
	}
	defer db.Close()
	if err = db.Ready(checkCtx); err != nil {
		cancel()
		fmt.Fprintln(errOut, "watch schema unavailable; run aegis migrate")
		return 1
	}
	cancel()
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	logger.Info("automatic evaluator started",
		"interval", interval.String(), "offline_after", offlineAfter.String(), "once", *once)
	for {
		if ctx.Err() != nil {
			return 0
		}
		err = runWatchCycle(ctx, db, *interval, *offlineAfter, logger)
		if err != nil {
			logger.Error("automatic evaluator cycle failed", "error_type", "cycle_failure")
			if *once {
				return 1
			}
		}
		if *once {
			return 0
		}
		// Use a timer AFTER the cycle. This avoids overlapping fleet scans,
		// bursts after slow database operations and accumulated stale ticks.
		tick := time.NewTimer(*interval)
		select {
		case <-ctx.Done():
			tick.Stop()
			return 0
		case <-tick.C:
		}
	}
}

func runWatchCycle(ctx context.Context, db *storage.Postgres,
	interval, offlineAfter time.Duration, logger *slog.Logger) error {
	leaseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	lease, locked, err := db.TryWatchLease(leaseCtx)
	cancel()
	if err != nil {
		return err
	}
	if !locked {
		logger.Info("automatic evaluator skipped; another worker owns watch lock")
		return nil
	}
	defer func() {
		if err := lease.Close(); err != nil {
			logger.Error("failed to release automatic evaluator lock", "error_type", "lock_release")
		}
	}()

	runCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	err = db.StartWatch(runCtx, interval, offlineAfter)
	stop()
	if err != nil {
		return err
	}
	stats, scanErr := watch.Cycle(ctx, db, offlineAfter, db.ProgressWatch)
	// A bounded final write survives shutdown requests; otherwise the
	// persisted state eventually appears stale rather than falsely green.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	finishErr := db.FinishWatch(finishCtx, stats, scanErr == nil)
	finishCancel()
	logger.Info("automatic evaluator scan finished",
		"agents", stats.AgentsChecked, "rules", stats.RulesChecked,
		"errors", stats.Errors, "complete", scanErr == nil)
	if finishErr != nil {
		return fmt.Errorf("persist watch status: %w", finishErr)
	}
	if scanErr != nil {
		return scanErr
	}
	if stats.Errors > 0 {
		return fmt.Errorf("one or more agents failed evaluation")
	}
	return nil
}
