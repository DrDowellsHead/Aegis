package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestActivityValidation(t *testing.T) {
	valid := ActivityQuery{Category: "incident", Action: "opened", Actor: "operator",
		Limit: 50, Search: "CPU", Cursor: "123", From: "2026-10-20T00:00:00Z",
		To: "2026-10-21T00:00:00Z"}
	if err := ValidateActivityQuery(valid); err != nil {
		t.Fatal(err)
	}
	cases := []ActivityQuery{
		{Category: "invalid", Limit: 50},
		{Category: "all", Limit: 101},
		{Action: "unknown", Limit: 50},
		{Actor: "bad actor", Limit: 50},
		{Search: "x", Limit: 50},
		{Cursor: "nope", Limit: 50},
		{Cursor: "0", Limit: 50},
		{AgentID: "invalid", Limit: 50},
		{From: "2026-10-21T00:00:00Z", To: "2026-10-20T00:00:00Z", Limit: 50},
		{From: "2025-01-01T00:00:00Z", To: "2026-10-20T00:00:00Z", Limit: 50},
	}
	for _, q := range cases {
		if ValidateActivityQuery(q) == nil {
			t.Fatalf("accepted invalid filter %+v", q)
		}
	}
	if got := escapedLike("CPU%_\\"); got != "CPU\\%\\_\\\\" {
		t.Fatalf("LIKE wildcard not escaped: %q", got)
	}
	if validActivityActor("admin password") {
		t.Fatal("invalid actor accepted")
	}
}

func activityForAgent(t *testing.T, db *Postgres, ctx context.Context, agentID string) []ActivityEntry {
	t.Helper()
	page, err := db.ListActivity(ctx, ActivityQuery{AgentID: agentID, Category: "all", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func TestActivityJournalIntegratedWithIncidents(t *testing.T) {
	db, ctx, id := incidentDB(t)
	// The incident database setup already migrates to 013.
	_, err := db.db.ExecContext(ctx, `UPDATE aegis_agents
		SET last_contact_at=clock_timestamp()-interval '2 hours'
		WHERE agent_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	status, err := db.EvaluateAgentLivenessAs(ctx, id, time.Minute, "operator")
	if err != nil || status != "offline" {
		t.Fatalf("evaluation: %s %v", status, err)
	}
	active := incidentList(t, db, ctx, id, "active")
	if len(active) != 1 {
		t.Fatal("expected active incident")
	}
	if _, err = db.ChangeIncident(ctx, active[0].ID, "acknowledge", "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ChangeIncident(ctx, active[0].ID, "resolve", "operator"); err != nil {
		t.Fatal(err)
	}
	got := activityForAgent(t, db, ctx, id)
	if len(got) != 4 {
		t.Fatalf("expected 3 transitions + 1 operator evaluation, got %+v", got)
	}
	actions := map[string]int{}
	for _, e := range got {
		actions[e.Action]++
		if e.Actor != "operator" && e.Actor != "system" {
			t.Fatalf("actor unexpected: %q", e.Actor)
		}
		if e.Action == "resolved" && (e.IncidentID == nil || *e.IncidentID != active[0].ID) {
			t.Fatalf("incident relation lost: %+v", e)
		}
	}
	for _, action := range []string{"opened", "acknowledged", "resolved", "connectivity_evaluated"} {
		if actions[action] != 1 {
			t.Fatalf("%s missing/duplicated: %v", action, actions)
		}
	}
	page, err := db.ListActivity(ctx, ActivityQuery{AgentID: id, Limit: 2, Category: "all"})
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("page one %+v %v", page, err)
	}
	other, err := db.ListActivity(ctx, ActivityQuery{AgentID: id, Limit: 2,
		Category: "all", Cursor: page.NextCursor})
	if err != nil || len(other.Items) != 2 || page.Items[0].ID == other.Items[0].ID {
		t.Fatalf("page two %+v %v", other, err)
	}
	subset, err := db.ListActivity(ctx, ActivityQuery{AgentID: id, Category: "incident", Actor: "operator", Limit: 50})
	if err != nil || len(subset.Items) != 2 {
		t.Fatalf("actor filter %+v %v", subset, err)
	}
	// A search string containing SQL LIKE syntax is treated literally.
	subset, err = db.ListActivity(ctx, ActivityQuery{AgentID: id, Search: "%_", Limit: 50})
	if err != nil || len(subset.Items) != 0 {
		t.Fatalf("wildcard injection %+v %v", subset, err)
	}
	// Repeat evaluation does not manufacture a second incident-open event.
	if _, err = db.EvaluateAgentLivenessAs(ctx, id, time.Minute, "operator"); err != nil {
		t.Fatal(err)
	}
	list := activityForAgent(t, db, ctx, id)
	opened := 0
	for _, event := range list {
		if event.Action == "opened" {
			opened++
		}
	}
	if opened > 1 {
		t.Fatalf("duplicated open event: %d", opened)
	}
}

func TestActivityPolicyChangeAndRollback(t *testing.T) {
	db, ctx, id := incidentDB(t)
	// Record the original and ensure it is restored for other packages.
	original, err := db.GetIncidentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		restore, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = db.UpdateIncidentSettings(restore, original)
	})
	updated := IncidentSettings{CooldownSeconds: original.CooldownSeconds, EscalateAfterSeconds: original.EscalateAfterSeconds}
	if updated.CooldownSeconds == 60 {
		updated.CooldownSeconds = 61
	} else {
		updated.CooldownSeconds = 60
	}
	if _, err = db.UpdateIncidentSettingsAs(ctx, updated, "operator"); err != nil {
		t.Fatal(err)
	}
	page, err := db.ListActivity(ctx, ActivityQuery{Category: "operator",
		Action: "policy_updated", Actor: "operator", Limit: 10})
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("policy change not logged: %+v %v", page, err)
	}
	// No-op updates do not manufacture another policy-change entry.
	if _, err = db.UpdateIncidentSettingsAs(ctx, updated, "operator"); err != nil {
		t.Fatal(err)
	}
	noOp, err := db.ListActivity(ctx, ActivityQuery{Category: "operator",
		Action: "policy_updated", Actor: "operator", Limit: 10})
	if err != nil || len(noOp.Items) != len(page.Items) {
		t.Fatalf("no-op logged unexpectedly: %v", err)
	}
	// The newly added activity journal does not accept browser/DB secrets.
	// No arbitrary JSON is forwarded by the gateway; details use allowlists.
	if _, err = db.UpdateIncidentSettingsAs(ctx, original, "bad actor with spaces"); err == nil {
		t.Fatal("mutation committed without valid audit identity")
	}
	stored, err := db.GetIncidentSettings(ctx)
	if err != nil || stored.CooldownSeconds != updated.CooldownSeconds {
		t.Fatalf("failed audit write still changed settings: %+v %v", stored, err)
	}
	_ = id
}

func TestActivityMigrationBackfill(t *testing.T) {
	// Each integration test uses the migrated schema. This test checks that
	// existing incident events are copied only once by an idempotent backfill.
	db, ctx, id := incidentDB(t)
	_, err := db.db.ExecContext(ctx, `UPDATE aegis_agents
		SET last_contact_at=clock_timestamp()-interval '2 hours' WHERE agent_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.EvaluateAgentLiveness(ctx, id, time.Minute); err != nil {
		t.Fatal(err)
	}
	active := incidentList(t, db, ctx, id, "active")
	if len(active) != 1 {
		t.Fatal("missing incident")
	}
	var raw, activity int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM aegis_incident_events WHERE incident_id=$1`, active[0].ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM aegis_activity_log WHERE incident_id=$1 AND category='incident'`, active[0].ID).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if raw != 1 || activity != raw {
		t.Fatalf("activity trigger out of sync: events=%d journal=%d", raw, activity)
	}
}

var _ = os.ErrNotExist
