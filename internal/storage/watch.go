package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// WatchStatus is a persisted snapshot of the latest automatic scan.
// Stale is computed using the database clock: old "running" records are
// not proof that the watch process remains alive.
type WatchStatus struct {
	State               string     `json:"state"`
	StartedAt           *time.Time `json:"started_at"`
	FinishedAt          *time.Time `json:"finished_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	ProgressAt          *time.Time `json:"progress_at"`
	AgentsChecked       int64      `json:"agents_checked"`
	RulesChecked        int64      `json:"rules_checked"`
	Errors              int64      `json:"errors"`
	IntervalSeconds     int        `json:"interval_seconds"`
	OfflineAfterSeconds int        `json:"offline_after_seconds"`
	Stale               bool       `json:"stale"`
}

type WatchProgress struct {
	AgentsChecked int64
	RulesChecked  int64
	Errors        int64
}

// WatchLease pins one PostgreSQL session to hold a session-level advisory
// lock for the duration of a scan, independent from nested SQL transactions.
type WatchLease struct {
	conn *sql.Conn
}

func (p *Postgres) TryWatchLease(ctx context.Context) (*WatchLease, bool, error) {
	if p == nil || p.db == nil {
		return nil, false, fmt.Errorf("database unavailable")
	}
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire watch connection: %w", err)
	}
	var locked bool
	if err = conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock(176341, 57)").Scan(&locked); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("acquire watch lock: %w", err)
	}
	if !locked {
		_ = conn.Close()
		return nil, false, nil
	}
	return &WatchLease{conn: conn}, true, nil
}

// Close unlocks using the exact same database session, even if the caller's
// context was canceled. Closing the connection releases the lock on crashes.
func (l *WatchLease) Close() error {
	if l == nil || l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRowContext(ctx,
		"SELECT pg_advisory_unlock(176341, 57)").Scan(&unlocked)
	if err != nil {
		// If the state of the session is unknown, close the physical
		// connection instead of returning a lock-holding pooled session.
		_ = conn.Raw(func(_ driver.Conn) error { return driver.ErrBadConn })
	}
	closeErr := conn.Close()
	if err != nil {
		return fmt.Errorf("release watch lock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("watch lock was not held")
	}
	return closeErr
}

func validateWatchConfig(interval, offlineAfter time.Duration) error {
	if interval < 10*time.Second || interval > time.Hour ||
		offlineAfter < 10*time.Second || offlineAfter > 24*time.Hour ||
		interval%time.Second != 0 || offlineAfter%time.Second != 0 {
		return fmt.Errorf("invalid watch interval or offline threshold")
	}
	return nil
}

func (p *Postgres) StartWatch(ctx context.Context, interval, offlineAfter time.Duration) error {
	if err := validateWatchConfig(interval, offlineAfter); err != nil {
		return err
	}
	if p == nil || p.db == nil {
		return fmt.Errorf("database unavailable")
	}
	_, err := p.db.ExecContext(ctx, `
		UPDATE aegis_watch_status SET
			state='running',started_at=clock_timestamp(),
			finished_at=NULL,progress_at=clock_timestamp(),
			agents_checked=0,rules_checked=0,errors=0,
			interval_seconds=$1,offline_after_seconds=$2
		WHERE id=1`, int(interval/time.Second), int(offlineAfter/time.Second))
	return err
}

func (p *Postgres) ProgressWatch(ctx context.Context, status WatchProgress) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("database unavailable")
	}
	_, err := p.db.ExecContext(ctx, `
		UPDATE aegis_watch_status SET
			progress_at=clock_timestamp(),agents_checked=$1,
			rules_checked=$2,errors=$3
		WHERE id=1 AND state='running'`,
		status.AgentsChecked, status.RulesChecked, status.Errors)
	return err
}

func (p *Postgres) FinishWatch(ctx context.Context, status WatchProgress, success bool) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("database unavailable")
	}
	state := "ok"
	switch {
	case !success:
		state = "failed"
	case status.Errors > 0:
		state = "partial_failure"
	}
	_, err := p.db.ExecContext(ctx, `
		UPDATE aegis_watch_status SET
			state=$1,finished_at=clock_timestamp(),progress_at=clock_timestamp(),
			last_success_at=CASE WHEN $1='ok' THEN clock_timestamp() ELSE last_success_at END,
			agents_checked=$2,rules_checked=$3,errors=$4
		WHERE id=1 AND state='running'`,
		state, status.AgentsChecked, status.RulesChecked, status.Errors)
	return err
}

func (p *Postgres) GetWatchStatus(ctx context.Context) (WatchStatus, error) {
	if p == nil || p.db == nil {
		return WatchStatus{}, fmt.Errorf("database unavailable")
	}
	var s WatchStatus
	var start, finish, last, progress sql.NullTime
	err := p.db.QueryRowContext(ctx, `
		SELECT state,started_at,finished_at,last_success_at,progress_at,
			agents_checked,rules_checked,errors,interval_seconds,
			offline_after_seconds,
			CASE WHEN progress_at IS NULL THEN TRUE ELSE
				clock_timestamp()>progress_at + (GREATEST(120,interval_seconds*3)*interval '1 second')
			END AS stale
		FROM aegis_watch_status WHERE id=1`).Scan(
		&s.State, &start, &finish, &last, &progress,
		&s.AgentsChecked, &s.RulesChecked, &s.Errors, &s.IntervalSeconds,
		&s.OfflineAfterSeconds, &s.Stale,
	)
	if err != nil {
		return WatchStatus{}, fmt.Errorf("read watch status: %w", err)
	}
	s.StartedAt = timePointer(start)
	s.FinishedAt = timePointer(finish)
	s.LastSuccessAt = timePointer(last)
	s.ProgressAt = timePointer(progress)
	return s, nil
}
