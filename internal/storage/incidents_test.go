package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/alerts"
)

func incidentDB(t *testing.T) (*Postgres, context.Context, string) {
	t.Helper()
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AEGIS_TEST_DATABASE_URL not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	db, err := Open(ctx, dsn)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		db.Close()
		cancel()
		t.Fatal(err)
	}
	if err = db.Ready(ctx); err != nil {
		db.Close()
		cancel()
		t.Fatal(err)
	}
	agentID := agentUUID(t)
	if _, err = db.db.ExecContext(ctx, `INSERT INTO aegis_agents(agent_id,registered)
		VALUES($1,TRUE)`, agentID); err != nil {
		db.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Journal entries deliberately have no cascading foreign keys to
		// historic incidents. Remove test-specific agent entries explicitly.
		_,_ = db.db.ExecContext(context.Background(),
			"DELETE FROM aegis_activity_log WHERE agent_id=$1",agentID)
		// Notification attempts/jobs reference incident transition events.
		_, _ = db.db.ExecContext(context.Background(),
			`DELETE FROM aegis_notification_attempts WHERE job_id IN
			  (SELECT j.id FROM aegis_notification_jobs j
			   JOIN aegis_incident_events e ON e.id=j.event_id
			   JOIN aegis_incidents i ON i.id=e.incident_id WHERE i.agent_id=$1)`, agentID)
		_, _ = db.db.ExecContext(context.Background(),
			`DELETE FROM aegis_notification_jobs WHERE event_id IN
			  (SELECT e.id FROM aegis_incident_events e
			   JOIN aegis_incidents i ON i.id=e.incident_id WHERE i.agent_id=$1)`, agentID)
		_, _ = db.db.ExecContext(context.Background(),
			"DELETE FROM aegis_incident_events WHERE incident_id IN (SELECT id FROM aegis_incidents WHERE agent_id=$1)", agentID)
		_, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_incidents WHERE agent_id=$1", agentID)
		_, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_threshold_rules WHERE agent_id=$1", agentID)
		_, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_measurements WHERE agent_id=$1", agentID)
		_, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_agents WHERE agent_id=$1", agentID)
		_ = db.Close()
		cancel()
	})
	return db, ctx, agentID
}
func makeCPU(t *testing.T, db *Postgres, ctx context.Context, id string, percent float64) {
	t.Helper()
	_, err := db.db.ExecContext(ctx, `INSERT INTO aegis_measurements
		(agent_id,measurement_id,schema_version,started_at,payload,payload_sha256,cpu_used_percent,projected)
		VALUES($1,$2,1,clock_timestamp()-interval '1 second',decode('','hex'),
			decode(repeat('00',32),'hex'),$3,TRUE)`, id, randomMeasurementID(t), percent)
	if err != nil {
		t.Fatal(err)
	}
}
func incidentList(t *testing.T, db *Postgres, ctx context.Context, id string, state string) []Incident {
	t.Helper()
	res, err := db.ListIncidents(ctx, IncidentQuery{AgentID: id, State: state, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	return res.Items
}
func TestThresholdIncidentLifecyclePostgres(t *testing.T) {
	db, ctx, id := incidentDB(t)
	// Step 5.3 intentionally tested immediate reopening. Opt out of the
	// step 5.4 cooldown to preserve that specific lifecycle scenario.
	setIncidentPolicyForTest(t, db, ctx, 0, 900)

	rule, err := db.CreateRule(ctx, RuleInput{AgentID: id, Rule: alerts.Rule{
		Name: "Critical CPU", Metric: "cpu", Direction: "above", FireAt: 85, RecoverAt: 70,
		WindowSeconds: 60, MinSamples: 1, MaxAgeSeconds: 60,
	}})
	if err != nil {
		t.Fatal(err)
	}
	makeCPU(t, db, ctx, id, 95)
	if _, err = db.EvaluateRules(ctx, id); err != nil {
		t.Fatal(err)
	}
	opened := incidentList(t, db, ctx, id, "active")
	if len(opened) != 1 || opened[0].State != "open" || opened[0].Kind != "threshold" ||
		opened[0].RuleID == nil || *opened[0].RuleID != rule.ID {
		t.Fatalf("bad threshold incident: %+v", opened)
	}
	// Concurrent evaluation must not duplicate the active incident.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := db.EvaluateRules(ctx, id); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := incidentList(t, db, ctx, id, "all"); len(got) != 1 {
		t.Fatalf("duplicate incident: %+v", got)
	}

	ack, err := db.ChangeIncident(ctx, opened[0].ID, "acknowledge", "operator")
	if err != nil || ack.State != "acknowledged" || ack.AcknowledgedAt == nil ||
		ack.AcknowledgedBy == nil || *ack.AcknowledgedBy != "operator" {
		t.Fatalf("ack: %+v %v", ack, err)
	}
	if _, err = db.ChangeIncident(ctx, ack.ID, "acknowledge", "operator"); !errors.Is(err, ErrIncidentTransition) {
		t.Fatalf("double acknowledgment: %v", err)
	}
	if _, err = db.EvaluateRules(ctx, id); err != nil {
		t.Fatal(err)
	}
	still := incidentList(t, db, ctx, id, "active")
	if len(still) != 1 || still[0].State != "acknowledged" {
		t.Fatalf("ack overwritten by evaluation: %+v", still)
	}

	_, err = db.db.ExecContext(ctx, "DELETE FROM aegis_measurements WHERE agent_id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	makeCPU(t, db, ctx, id, 50)
	if _, err = db.EvaluateRules(ctx, id); err != nil {
		t.Fatal(err)
	}
	resolved := incidentList(t, db, ctx, id, "resolved")
	if len(resolved) != 1 || resolved[0].State != "resolved" || resolved[0].ResolvedAt == nil ||
		resolved[0].Resolution == nil || *resolved[0].Resolution != "condition_recovered" {
		t.Fatalf("rule did not recover: %+v", resolved)
	}
	if _, err = db.ChangeIncident(ctx, ack.ID, "resolve", "operator"); !errors.Is(err, ErrIncidentTransition) {
		t.Fatalf("resolved incident modified: %v", err)
	}
	events, err := db.ListIncidentEvents(ctx, ack.ID)
	if err != nil || len(events) != 3 ||
		events[0].Action != "opened" || events[1].Action != "acknowledged" ||
		events[2].Action != "resolved" {
		t.Fatalf("events: %+v %v", events, err)
	}
	_, err = db.db.ExecContext(ctx, "DELETE FROM aegis_measurements WHERE agent_id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	makeCPU(t, db, ctx, id, 96)
	if _, err = db.EvaluateRules(ctx, id); err != nil {
		t.Fatal(err)
	}
	reopened := incidentList(t, db, ctx, id, "active")
	if len(reopened) != 1 || reopened[0].ID == ack.ID {
		t.Fatalf("new breach must have new ID: %+v", reopened)
	}
	// Removing a rule resolves its active incident without erasing history.
	found, err := db.DeleteRule(ctx, rule.ID)
	if err != nil || !found {
		t.Fatalf("delete rule: %v %v", found, err)
	}
	all := incidentList(t, db, ctx, id, "all")
	if len(all) != 2 || all[0].State != "resolved" || all[0].Resolution == nil ||
		*all[0].Resolution != "rule_deleted" || all[0].RuleID != nil {
		t.Fatalf("rule deletion lost incident history: %+v", all)
	}
}
func TestOfflineIncidentLifecyclePostgres(t *testing.T) {
	db, ctx, id := incidentDB(t)
	state, err := db.EvaluateAgentLiveness(ctx, id, time.Minute)
	if err != nil || state != "unknown" || len(incidentList(t, db, ctx, id, "all")) != 0 {
		t.Fatalf("unknown agent generated incident: %s %v", state, err)
	}
	_, err = db.db.ExecContext(ctx, `UPDATE aegis_agents SET last_contact_at=
		clock_timestamp()-interval '2 hours' WHERE agent_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	state, err = db.EvaluateAgentLiveness(ctx, id, time.Minute)
	if err != nil || state != "offline" {
		t.Fatalf("offline evaluation: %s %v", state, err)
	}
	inc := incidentList(t, db, ctx, id, "active")
	if len(inc) != 1 || inc[0].Kind != "agent_offline" {
		t.Fatalf("offline incident: %+v", inc)
	}
	state, err = db.EvaluateAgentLiveness(ctx, id, time.Minute)
	if err != nil || state != "offline" || len(incidentList(t, db, ctx, id, "all")) != 1 {
		t.Fatalf("offline created duplicates: %s %v", state, err)
	}
	// A valid received heartbeat resets contact: incident closes only on
	// the next evaluation, not by modifying the telemetry rows.
	if err = db.RecordHeartbeat(ctx, id); err != nil {
		t.Fatal(err)
	}
	state, err = db.EvaluateAgentLiveness(ctx, id, time.Minute)
	if err != nil || state != "online" {
		t.Fatalf("online evaluation: %s %v", state, err)
	}
	res := incidentList(t, db, ctx, id, "resolved")
	if len(res) != 1 || res[0].Resolution == nil || *res[0].Resolution != "contact_restored" {
		t.Fatalf("recovered agent did not resolve incident: %+v", res)
	}
	if _, err = db.ChangeIncident(ctx, inc[0].ID, "acknowledge", "operator"); !errors.Is(err, ErrIncidentTransition) {
		t.Fatalf("resolved offline incident mutable: %v", err)
	}
}
func TestManualResolveAndPaginationPostgres(t *testing.T) {
	db, ctx, id := incidentDB(t)
	// Step 5.3 intentionally tested immediate reopening. Opt out of the
	// step 5.4 cooldown to preserve that specific lifecycle scenario.
	setIncidentPolicyForTest(t, db, ctx, 0, 900)

	_, err := db.db.ExecContext(ctx, `UPDATE aegis_agents SET last_contact_at=
		clock_timestamp()-interval '2 hours' WHERE agent_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.EvaluateAgentLiveness(ctx, id, time.Minute); err != nil {
		t.Fatal(err)
	}
	one := incidentList(t, db, ctx, id, "active")
	record, err := db.ChangeIncident(ctx, one[0].ID, "resolve", "local-operator")
	if err != nil || record.State != "resolved" || *record.Resolution != "operator_resolved" {
		t.Fatalf("manual resolve: %+v %v", record, err)
	}
	// Manually resolving a still-active condition creates a new incident
	// at the NEXT evaluation; suppression is outside step 5.3.
	if _, err = db.EvaluateAgentLiveness(ctx, id, time.Minute); err != nil {
		t.Fatal(err)
	}
	active := incidentList(t, db, ctx, id, "active")
	if len(active) != 1 || active[0].ID == one[0].ID {
		t.Fatalf("manual resolve/reopen: %+v", active)
	}
	first, err := db.ListIncidents(ctx, IncidentQuery{State: "all", AgentID: id, Limit: 1})
	if err != nil || len(first.Items) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("pagination first page: %+v %v", first, err)
	}
	second, err := db.ListIncidents(ctx, IncidentQuery{State: "all", AgentID: id, Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("pagination second page: %+v %v", second, err)
	}
	if _, err = db.ListIncidents(ctx, IncidentQuery{State: "all", Limit: 10, Cursor: "bad"}); err == nil {
		t.Fatal("malformed incident cursor accepted")
	}
}

func setIncidentPolicyForTest(t *testing.T, db *Postgres, ctx context.Context, cooldown, escalate int) {
	t.Helper()
	old, err := db.GetIncidentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateIncidentSettings(ctx, IncidentSettings{
		CooldownSeconds: cooldown, EscalateAfterSeconds: escalate,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		back, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := db.UpdateIncidentSettings(back, old)
		if err != nil {
			t.Errorf("restore incident policy: %v", err)
		}
	})
}
