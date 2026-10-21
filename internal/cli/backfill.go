package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/adminconfig"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// backfill is intentionally an explicit operator command: schema migrations
// must not hold a database-wide transaction while rewriting old telemetry.
func runBackfill(ctx context.Context, args []string, out, errOut io.Writer) (code int) {
	cfg, err := adminconfig.Load(os.Getenv("AEGIS_ADMIN_CONFIG"))
	if err != nil {
		fmt.Fprintln(errOut, "invalid administrative configuration:", err)
		return 2
	}
	flags := flag.NewFlagSet("backfill", flag.ContinueOnError)
	flags.SetOutput(errOut)
	batchSize := flags.Int("batch-size", cfg.BackfillBatch, "records per transaction (1..500)")
	maxRecords := flags.Int("max-records", 0, "stop after this many rows (0 = all)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *batchSize < 1 || *batchSize > 500 || *maxRecords < 0 {
		fmt.Fprintln(errOut, "backfill requires batch-size 1..500 and max-records >= 0")
		return 2
	}
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	db, err := storage.Open(connCtx, os.Getenv("AEGIS_DATABASE_URL"))
	cancel()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer db.Close()
	readyCtx, readyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer readyCancel()
	if err := db.Ready(readyCtx); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	id, err := db.BeginAudit(ctx, "backfill", adminOperator(), "unprojected", nil)
	if err != nil {
		fmt.Fprintln(errOut, "audit unavailable; refusing backfill:", err)
		return 1
	}
	state := "failed"
	defer func() {
		if ctx.Err() != nil && state != "succeeded" {
			state = "interrupted"
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.FinishAudit(finishCtx, id, state); err != nil {
			fmt.Fprintln(errOut, "cannot finalize audit:", err)
			code = 1
		}
	}()
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		n := *batchSize
		if *maxRecords > 0 && *maxRecords-total < n {
			n = *maxRecords - total
		}
		if n == 0 {
			break
		}
		processed, err := db.BackfillBatchAudited(ctx, n, id)
		if err != nil {
			fmt.Fprintln(errOut, "backfill:", err)
			return 1
		}
		total += processed
		if processed == 0 {
			break
		}
	}
	state = "succeeded"
	fmt.Fprintf(out, "Backfilled %d legacy measurements (operation %s)\n", total, id)
	return 0
}
