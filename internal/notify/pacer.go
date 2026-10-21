package notify

import (
	"context"
	"fmt"
	"time"
)

// Pacer limits the rate of notification delivery attempts in ONE worker.
// It is used before claiming a database lease, never while holding one.
// Multiple workers have independent budgets; SQL leases remain authoritative.
type Pacer struct {
	interval time.Duration
	next     time.Time
}

func NewPacer(perMinute int) (*Pacer, error) {
	if perMinute < 1 || perMinute > 600 {
		return nil, fmt.Errorf("notification delivery rate must be 1..600 per minute")
	}
	return &Pacer{interval: time.Minute / time.Duration(perMinute)}, nil
}

// Wait is cancelable. It does not move the deadline: only a claimed
// delivery job consumes a permit, so idle polls cannot delay the next job.
func (p *Pacer) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.next.IsZero() {
		return nil
	}
	delay := time.Until(p.next)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// NoteAttempt starts the next rate interval after a job has been claimed.
// All outcomes (including failed deliveries) count against the rate.
func (p *Pacer) NoteAttempt() {
	p.next = time.Now().Add(p.interval)
}
