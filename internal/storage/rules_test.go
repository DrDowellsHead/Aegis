package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/alerts"
)

func TestRuleEvaluationPostgres(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" { t.Skip("AEGIS_TEST_DATABASE_URL not set") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil { t.Fatal(err) }
	defer db.Close()
	if err := db.Migrate(ctx); err != nil { t.Fatal(err) }
	if err := db.Ready(ctx); err != nil { t.Fatal(err) }
	id := agentUUID(t)
	if err := db.SyncAgents(ctx, []string{id}); err != nil { t.Fatal(err) }
	defer func() {
		_, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_threshold_rules WHERE agent_id=$1", id)
	}()
	input := RuleInput{
		AgentID: id,
		Rule: alerts.Rule{Name: "High CPU", Metric: "cpu", Direction: "above",
			FireAt: 85, RecoverAt: 70, WindowSeconds: 600, MinSamples: 3, MaxAgeSeconds: 300},
	}
	r, err := db.CreateRule(ctx, input)
	if err != nil { t.Fatal(err) }
	if _, err := db.CreateRule(ctx, input); !errors.Is(err, ErrRuleExists) {
		t.Fatalf("duplicate name: %v", err)
	}
	rows, err := db.ListRules(ctx, id)
	if err != nil || len(rows) != 1 || rows[0].ID != r.ID { t.Fatalf("list: %v %+v", err, rows) }
	insert := func(at time.Time, cpu *float64, path string, disk *float64) {
		t.Helper()
		_, err := db.db.ExecContext(ctx, `INSERT INTO aegis_measurements
			(agent_id,measurement_id,schema_version,started_at,payload,payload_sha256,
			 cpu_used_percent,disk_used_percent,disk_path,projected)
			VALUES($1,$2,1,$3,decode('','hex'),decode(repeat('00',32),'hex'),$4,$5,$6,TRUE)`,
			id, randomMeasurementID(t), at, cpu, disk, path)
		if err != nil { t.Fatal(err) }
	}
	now := time.Now().UTC()
	result, err := db.EvaluateRules(ctx, id)
	if err != nil || len(result) != 1 || result[0].LastStatus != "insufficient_data" || result[0].Firing {
		t.Fatalf("empty rule: %v %+v", err, result)
	}
	v := 90.0
	insert(now.Add(-3*time.Minute), &v, "", nil)
	insert(now.Add(-2*time.Minute), &v, "", nil)
	insert(now.Add(-time.Minute), &v, "", nil)
	result, err = db.EvaluateRules(ctx, id)
	if err != nil || !result[0].Firing || result[0].LastStatus != "firing" || result[0].ChangedAt == nil {
		t.Fatalf("should fire: %v %+v", err, result)
	}
	change := *result[0].ChangedAt
	result, err = db.EvaluateRules(ctx, id)
	if err != nil || !result[0].ChangedAt.Equal(change) {
		t.Fatalf("duplicate transition: %v %+v", err, result)
	}
	// Backlogged historical data must not resolve a firing rule.
	old := now.Add(-24*time.Hour)
	v = 1
	insert(old, &v, "", nil)
	result, err = db.EvaluateRules(ctx, id)
	if err != nil || !result[0].Firing { t.Fatalf("stale sample resolved rule: %v %+v", err, result) }
	// Isolate a recovery window by removing the three first test measurements.
	_, err = db.db.ExecContext(ctx, "DELETE FROM aegis_measurements WHERE agent_id=$1 AND started_at > $2", id, now.Add(-10*time.Minute))
	if err != nil { t.Fatal(err) }
	v = 65
	for i := 1; i <= 3; i++ { insert(time.Now().UTC().Add(-time.Duration(i)*time.Second), &v, "", nil) }
	result, err = db.EvaluateRules(ctx, id)
	if err != nil || result[0].Firing || result[0].LastStatus != "normal" ||
		result[0].ChangedAt == nil || !result[0].ChangedAt.After(change) {
		t.Fatalf("should recover: %v %+v", err, result)
	}
	if found, err := db.DeleteRule(ctx, r.ID); err != nil || !found { t.Fatalf("delete: %v %v", found, err) }
	if found, err := db.DeleteRule(ctx, r.ID); err != nil || found { t.Fatalf("delete idempotent: %v %v", found, err) }
}

func TestDiskRuleIsolationPostgres(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" { t.Skip("AEGIS_TEST_DATABASE_URL not set") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil { t.Fatal(err) }
	defer db.Close()
	if err := db.Migrate(ctx); err != nil { t.Fatal(err) }
	id := agentUUID(t)
	if err := db.SyncAgents(ctx, []string{id}); err != nil { t.Fatal(err) }
	defer func() {
		_, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_threshold_rules WHERE agent_id=$1", id)
	}()
	rule, err := db.CreateRule(ctx, RuleInput{AgentID:id, Rule: alerts.Rule{
		Name:"Root disk",Metric:"disk",DiskPath:"/",Direction:"above",FireAt:80,RecoverAt:60,
		WindowSeconds:300,MinSamples:2,MaxAgeSeconds:120,
	}})
	if err != nil { t.Fatal(err) }
	defer db.DeleteRule(context.Background(), rule.ID)
	now := time.Now().UTC()
	for i:=0;i<5;i++ {
		path := "/other"
		if i==0 { path="/" }
		_,err := db.db.ExecContext(ctx, `INSERT INTO aegis_measurements
			(agent_id,measurement_id,schema_version,started_at,payload,payload_sha256,
			disk_used_percent,disk_path,projected)
			VALUES($1,$2,1,$3,decode('','hex'),decode(repeat('00',32),'hex'),95,$4,TRUE)`,
			id,randomMeasurementID(t),now.Add(-time.Duration(i+1)*time.Second),path)
		if err != nil { t.Fatal(err) }
	}
	results,err := db.EvaluateRules(ctx,id)
	if err != nil || len(results)!=1 || results[0].Firing ||
		results[0].LastStatus!="insufficient_data" || results[0].SampleCount!=1 {
		t.Fatalf("disk paths mixed: %v %+v",err,results)
	}
}
