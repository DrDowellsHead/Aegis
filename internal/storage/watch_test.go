package storage

import (
	"context"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/alerts"
)

func TestWatchConfigurationValidation(t *testing.T) {
	for _, tc := range []struct{ interval, offline time.Duration }{
		{10 * time.Second, 10 * time.Second},
		{30 * time.Second, time.Minute},
		{time.Hour, 24 * time.Hour},
	} {
		if err := validateWatchConfig(tc.interval, tc.offline); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ interval, offline time.Duration }{
		{time.Second, time.Minute},
		{time.Hour + time.Second, time.Minute},
		{30 * time.Second, 5 * time.Second},
		{30 * time.Second, 25 * time.Hour},
		{10500 * time.Millisecond, time.Minute},
	} {
		if err := validateWatchConfig(tc.interval, tc.offline); err == nil {
			t.Fatalf("accepted invalid watch config %+v", tc)
		}
	}
}

func TestWatchAdvisoryLeasePostgres(t *testing.T) {
	db, ctx, _ := incidentDB(t)
	first, ok, err := db.TryWatchLease(ctx)
	if err != nil || !ok || first == nil {
		t.Fatalf("first watcher could not lock: %v %v", ok, err)
	}
	second, ok, err := db.TryWatchLease(ctx)
	if err != nil || ok || second != nil {
		t.Fatalf("second watcher acquired same lock: %v %v", ok, err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	third, ok, err := db.TryWatchLease(ctx)
	if err != nil || !ok || third == nil {
		t.Fatalf("lock did not release: %v %v", ok, err)
	}
	if err = third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWatchAutomaticEvaluationAndStatusPostgres(t *testing.T) {
	db, ctx, id := incidentDB(t)
	setIncidentPolicyForTest(t, db, ctx, 0, 60)
	_, err := db.CreateRule(ctx, RuleInput{AgentID: id, Rule: alerts.Rule{
		Name: "CPU high", Metric: "cpu", Direction: "above", FireAt: 85, RecoverAt: 70,
		WindowSeconds: 60, MinSamples: 1, MaxAgeSeconds: 60,
	}})
	if err != nil {
		t.Fatal(err)
	}
	makeCPU(t, db, ctx, id, 96)
	_, err = db.db.ExecContext(ctx, `UPDATE aegis_agents SET last_contact_at=
		clock_timestamp()-interval '2 hours' WHERE agent_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.StartWatch(ctx, 30*time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = db.EvaluateRulesAs(ctx, id, "scheduler"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.EvaluateAgentLivenessAs(ctx, id, time.Minute, "scheduler"); err != nil {
		t.Fatal(err)
	}
	open := incidentList(t, db, ctx, id, "active")
	if len(open) != 2 {
		t.Fatalf("rule and offline incidents not created: %+v", open)
	}
	events := activityForAgent(t, db, ctx, id)
	seen := map[string]int{}
	for _, event := range events {
		seen[event.Action]++
	}
	if seen["opened"] != 2 {
		t.Fatalf("missing automatic open events: %v", seen)
	}
	if seen["rules_evaluated"] != 0 || seen["connectivity_evaluated"] != 0 {
		t.Fatalf("scheduler flooded manual operator audit: %v", seen)
	}
	p := WatchProgress{AgentsChecked: 1, RulesChecked: 1}
	if err = db.ProgressWatch(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishWatch(ctx, p, true); err != nil {
		t.Fatal(err)
	}
	state, err := db.GetWatchStatus(ctx)
	if err != nil || state.State != "ok" || state.Stale ||
		state.AgentsChecked != 1 || state.RulesChecked != 1 || state.LastSuccessAt == nil {
		t.Fatalf("bad watch status: %+v %v", state, err)
	}
	if err = db.StartWatch(ctx, time.Minute, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishWatch(ctx, WatchProgress{AgentsChecked: 1, Errors: 1}, true); err != nil {
		t.Fatal(err)
	}
	state, err = db.GetWatchStatus(ctx)
	if err != nil || state.State != "partial_failure" || state.Errors != 1 {
		t.Fatalf("missing partial failure status: %+v %v", state, err)
	}
	// Simulate a stopped process; the SQL clock determines staleness.
	_, err = db.db.ExecContext(ctx, `UPDATE aegis_watch_status SET
		progress_at=clock_timestamp()-interval '1 hour' WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	state, err = db.GetWatchStatus(ctx)
	if err != nil || !state.Stale {
		t.Fatalf("stale process appears healthy: %+v %v", state, err)
	}
}
func TestWatchCancellationReleasesLockPostgres(t *testing.T) {
	db, ctx, _ := incidentDB(t)
	cancelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lease, ok, err := db.TryWatchLease(cancelCtx)
	if err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	cancel()
	if err = lease.Close(); err != nil {
		t.Fatalf("release after cancel: %v", err)
	}
	other, ok, err := db.TryWatchLease(context.Background())
	if err != nil || !ok {
		t.Fatalf("lock leaked after cancel: %v %v", ok, err)
	}
	if err = other.Close(); err != nil {
		t.Fatal(err)
	}
}
