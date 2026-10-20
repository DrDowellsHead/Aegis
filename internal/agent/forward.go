package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/spool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sender is owned by its consumer. It must reuse Record.MeasurementID across retries.
type Sender interface {
	Send(context.Context, spool.Record) error
}

type ForwardOptions struct {
	Interval       time.Duration
	Count          int // zero means run until cancellation
	RequestTimeout time.Duration
	RetryMin       time.Duration
	RetryMax       time.Duration
}

func (o ForwardOptions) validate() error {
	if o.Interval < 100*time.Millisecond || o.Interval > 24*time.Hour ||
		o.Count < 0 || o.RequestTimeout <= 0 || o.RequestTimeout > time.Minute ||
		o.RetryMin <= 0 || o.RetryMax < o.RetryMin || o.RetryMax > time.Hour {
		return fmt.Errorf("invalid forwarding interval/count/timeout/retry configuration")
	}
	return nil
}

// Forward runs exactly one collector goroutine and one sender loop.
// Only an authenticated server's durable database ACK removes an outbox entry.
// If an ACK is lost, replay is safe because the DB deduplicates by stable ID.
func Forward(parent context.Context, source Source, sender Sender, q *spool.Queue, logger *slog.Logger, opts ForwardOptions) error {
	if source == nil || sender == nil || q == nil || logger == nil {
		return fmt.Errorf("forward dependencies are required")
	}
	if err := opts.validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	wakeup := make(chan struct{}, 1)
	done := make(chan struct{})
	var producerErr error
	go func() {
		defer close(done)
		producerErr = collectLoop(ctx, source, q, logger, opts, wakeup)
	}()
	sendErr := sendLoop(ctx, sender, q, logger, opts, wakeup, done, &producerErr)
	cancel()
	<-done // ensure collector is joined before return
	if parent.Err() != nil {
		return nil
	} // Ctrl+C is a clean stop
	if sendErr != nil {
		return sendErr
	}
	return producerErr
}

func collectLoop(ctx context.Context, source Source, q *spool.Queue, logger *slog.Logger, opts ForwardOptions, wakeup chan<- struct{}) error {
	for n := 0; opts.Count == 0 || n < opts.Count; n++ {
		if ctx.Err() != nil {
			return nil
		}
		snapshot, err := source.Collect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("collect metrics: %w", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		record, err := q.Append(snapshot)
		if err != nil {
			if errors.Is(err, spool.ErrFull) {
				logger.Error("spool full: metrics collection stopped without deleting old records")
			}
			return fmt.Errorf("enqueue metrics: %w", err)
		}
		logger.Info("measurement saved to disk", "measurement_id", record.MeasurementID, "partial", len(snapshot.Errors) > 0)
		select {
		case wakeup <- struct{}{}:
		default:
		}
		if opts.Count > 0 && n+1 == opts.Count {
			return nil
		}
		timer := time.NewTimer(opts.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func retryDelay(failures int, min, max time.Duration) time.Duration {
	delay := min
	for n := 1; n < failures; n++ {
		if delay >= max/2 {
			delay = max
			break
		}
		delay *= 2
		if delay >= max {
			delay = max
			break
		}
	}
	// Jitter in [0.8, 1.0] of the bounded backoff: never exceeds max.
	return time.Duration(float64(delay) * (0.8 + rand.Float64()*0.2))
}

func nonRetryable(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.FailedPrecondition, codes.Unimplemented:
		return true
	default:
		return false
	}
}

func sendLoop(ctx context.Context, sender Sender, q *spool.Queue, logger *slog.Logger, opts ForwardOptions, wakeup <-chan struct{}, done <-chan struct{}, producerErr *error) error {
	completed := false
	failures := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		if !completed {
			select {
			case <-done:
				completed = true
				done = nil
				if *producerErr != nil {
					return *producerErr
				}
			default:
			}
		}
		next, hasNext := q.Peek()
		if !hasNext {
			if completed {
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-wakeup:
			case <-done:
			}
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, opts.RequestTimeout)
		err := sender.Send(callCtx, next)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			if err := q.Ack(next.MeasurementID); err != nil {
				return fmt.Errorf("durably stored remotely, but local spool removal failed: %w", err)
			}
			failures = 0
			logger.Info("durably stored; local record removed", "measurement_id", next.MeasurementID)
			continue
		}
		if nonRetryable(err) {
			return fmt.Errorf("non-retryable telemetry error (spool retained): %w", err)
		}
		failures++
		delay := retryDelay(failures, opts.RetryMin, opts.RetryMax)
		logger.Warn("telemetry unavailable; will retry", "error", err, "retry_after", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		case <-done:
			// Do not mistake collection completion for successful delivery.
			done = nil
			completed = true
			if *producerErr != nil {
				timer.Stop()
				return *producerErr
			}
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil
			}
		}
	}
}
