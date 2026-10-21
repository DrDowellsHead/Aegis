package storage

import (
	"context"
	"crypto/sha256"
	"os"
	"testing"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateActor(t *testing.T) {
	for _, ok := range []string{"local-operator", "admin@example.org", "ops_1"} {
		if err := ValidateActor(ok); err != nil {
			t.Errorf("rejected actor %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " contains spaces", "a/b", "password=secret"} {
		if err := ValidateActor(bad); err == nil {
			t.Errorf("accepted actor %q", bad)
		}
	}
}

func TestAuditedRetentionRollbackAndResume(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration requires AEGIS_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	agent := agentUUID(t)
	for i := 0; i < 2; i++ {
		id := randomMeasurementID(t)
		sample := &pb.Snapshot{SchemaVersion: 1,
			StartedAt: timestamppb.New(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
			Os:        "linux", Arch: "amd64"}
		if _, err := db.Save(ctx, agent, id, sample); err != nil {
			t.Fatal(err)
		}
		_, err = db.db.ExecContext(ctx, `
          UPDATE aegis_measurements SET received_at=clock_timestamp()-interval '50 days'
          WHERE agent_id=$1 AND measurement_id=$2
        `, agent, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	plan, err := db.Preview(ctx, RetentionFilter{Days: 30, AgentID: agent})
	if err != nil || plan.EligibleRows != 2 {
		t.Fatalf("preview=%+v err=%v", plan, err)
	}
	first, err := db.BeginAudit(ctx, "retention", "test-operator", agent, &plan.RetentionDays)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := db.PurgeAudited(ctx, plan, 1, 1, first)
	if err != nil || deleted != 1 {
		t.Fatalf("first deletion=%d err=%v", deleted, err)
	}
	// Nonexistent audit causes its batch transaction to roll back its DELETE.
	missingAudit := "123e4567-e89b-42d3-a456-426614174099"
	deleted, err = db.PurgeAudited(ctx, plan, 1, 1, missingAudit)
	if err == nil || deleted != 0 {
		t.Fatalf("missing audit permitted untracked DELETE: %d %v", deleted, err)
	}
	var remain int
	if err := db.db.QueryRowContext(ctx, `
        SELECT count(*) FROM aegis_measurements WHERE agent_id=$1 AND received_at<$2
    `, agent, plan.Cutoff).Scan(&remain); err != nil {
		t.Fatal(err)
	}
	if remain != 1 {
		t.Fatalf("lost sample during rolled back audit: %d", remain)
	}
	more, err := db.PurgeAudited(ctx, plan, 1, 10, first)
	if err != nil || more != 1 {
		t.Fatalf("resumed deletion=%d err=%v", more, err)
	}
	if err := db.FinishAudit(ctx, first, "succeeded"); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishAudit(ctx, first, "failed"); err == nil {
		t.Fatal("final audit was mutable")
	}
	events, err := db.ListAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.ID == first {
			found = true
			if event.Action != "retention" || event.Status != "succeeded" ||
				event.AffectedRows != 2 || event.FinishedAt == nil || event.Actor != "test-operator" {
				t.Fatalf("wrong committed audit progress: %+v", event)
			}
		}
	}
	if !found {
		t.Fatalf("operation %s not found in audit", first)
	}
}

func TestAuditedBackfillAndSecretValidation(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL integration requires AEGIS_TEST_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginAudit(ctx, "retention", "secret=password", "all", nil); err == nil {
		t.Fatal("audit accepted secret-shaped actor")
	}
	agent := agentUUID(t)
	id := randomMeasurementID(t)
	sample := &pb.Snapshot{SchemaVersion: 1, StartedAt: timestamppb.New(
		time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
		Os: "linux", Arch: "amd64", Cpu: &pb.CPU{UsedPercent: 33},
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(wire)
	if _, err := db.db.ExecContext(ctx, `
        INSERT INTO aegis_measurements(agent_id,measurement_id,schema_version,started_at,payload,payload_sha256)
        VALUES($1,$2,1,$3,$4,$5)
    `, agent, id, sample.StartedAt.AsTime(), wire, hash[:]); err != nil {
		t.Fatal(err)
	}
	auditID, err := db.BeginAudit(ctx, "backfill", "test-operator", "unprojected", nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := db.BackfillBatchAudited(ctx, 500, auditID)
	if err != nil || n < 1 {
		t.Fatalf("audited backfill=%d err=%v", n, err)
	}
	if err := db.FinishAudit(ctx, auditID, "succeeded"); err != nil {
		t.Fatal(err)
	}
	var projected bool
	if err := db.db.QueryRowContext(ctx, `
        SELECT projected FROM aegis_measurements WHERE agent_id=$1 AND measurement_id=$2
    `, agent, id).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if !projected {
		t.Fatal("backfill was not committed")
	}
	events, err := db.ListAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.ID == auditID {
			if ev.AffectedRows != int64(n) {
				t.Fatalf("audit progress=%d rows=%d", ev.AffectedRows, n)
			}
			return
		}
	}
	t.Fatal("audited backfill missing")
}
