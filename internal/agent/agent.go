// Package agent schedules local collection and writes one JSON object per line.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

// Source is defined by the consumer so tests need neither OS counters nor disks.
type Source interface {
	Collect(context.Context) (metrics.Snapshot, error)
}

var ErrPartial = errors.New("some measurements were unavailable; see errors in JSON")

// Run collects immediately, then waits interval after each completed sample.
// count=0 streams until cancellation. Slow collection/output delays the next
// sample; there is no unbounded queue and collections never overlap.
// Output is synchronous: arbitrary blocked writers cannot be canceled by context.
func Run(ctx context.Context, source Source, output io.Writer, interval time.Duration, count int) error {
	if source == nil || output == nil {
		return fmt.Errorf("source and output are required")
	}
	if interval <= 0 || interval > 24*time.Hour {
		return fmt.Errorf("interval must be in (0, 24h]")
	}
	if count < 0 {
		return fmt.Errorf("count must not be negative")
	}
	encoder := json.NewEncoder(output)
	partial := false
	for n := 0; count == 0 || n < count; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot, err := source.Collect(ctx)
		if err != nil {
			return fmt.Errorf("collect metrics: %w", err)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = encoder.Encode(snapshot); err != nil {
			return fmt.Errorf("write metrics: %w", err)
		}
		partial = partial || len(snapshot.Errors) > 0
		if count > 0 && n+1 == count {
			break
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if partial {
		return ErrPartial
	}
	return nil
}
