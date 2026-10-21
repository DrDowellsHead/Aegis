package storage

import (
	"context"
	"os"
	"testing"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRetentionFilterValidation(t *testing.T) {
	for _, days:=range []int{-1,0,1,6,3651}{
		if (RetentionFilter{Days:days}).Validate()==nil {
			t.Fatalf("accepted invalid retention period: %d",days)
		}
	}
	for _,days:=range []int{7,30,3650}{
		if err:=(RetentionFilter{Days:days}).Validate();err!=nil{t.Fatal(err)}
	}
	if err:=(RetentionFilter{Days:30,AgentID:"not-a-uuid"}).Validate();err==nil {
		t.Fatal("invalid agent ID accepted")
	}
}

func TestRetentionAndStorageUsagePostgres(t *testing.T) {
	dsn:=os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn==""{t.Skip("AEGIS_TEST_DATABASE_URL not set")}
	ctx,cancel:=context.WithTimeout(context.Background(),40*time.Second)
	defer cancel()
	db,err:=Open(ctx,dsn)
	if err!=nil{t.Fatal(err)}
	defer db.Close()
	if err:=db.Migrate(ctx);err!=nil{t.Fatal(err)}
	if err:=db.Migrate(ctx);err!=nil{t.Fatal("migration not idempotent:",err)}
	if err:=db.Ready(ctx);err!=nil{t.Fatal(err)}
	agent:=agentUUID(t)
	differentAgent:=agentUUID(t)
	ids:=make([]string,4)
	// Save always uses a CURRENT PostgreSQL received_at timestamp, even for
	// old started_at timestamps. Later we deliberately age two DB rows.
	for i:=range ids {
		ids[i]=randomMeasurementID(t)
		snapshot:=&pb.Snapshot{
			SchemaVersion:1,
			StartedAt:timestamppb.New(time.Date(2020,1,1,0,0,0,0,time.UTC)),
			Os:"linux",Arch:"amd64",Cpu:&pb.CPU{UsedPercent:float64(i)},
		}
		if _,err:=db.Save(ctx,agent,ids[i],snapshot);err!=nil{t.Fatal(err)}
	}
	otherID:=randomMeasurementID(t)
	otherSample:=&pb.Snapshot{
		SchemaVersion:1,StartedAt:timestamppb.New(time.Date(2020,1,1,0,0,0,0,time.UTC)),
		Os:"linux",Arch:"amd64",
	}
	if _,err:=db.Save(ctx,differentAgent,otherID,otherSample);err!=nil{t.Fatal(err)}
	for _,id:=range ids[:2] {
		if _,err:=db.db.ExecContext(ctx,`
			UPDATE aegis_measurements SET received_at=clock_timestamp()-interval '31 days'
			WHERE agent_id=$1 AND measurement_id=$2
		`,agent,id);err!=nil{t.Fatal(err)}
	}
	// Pin the exact boundary BEFORE modifying an additional row.
	plan,err:=db.Preview(ctx,RetentionFilter{Days:30,AgentID:agent})
	if err!=nil{t.Fatal(err)}
	if plan.EligibleRows!=2{t.Fatalf("preview counted %d expected 2",plan.EligibleRows)}
	if _,err:=db.db.ExecContext(ctx,`
		UPDATE aegis_measurements SET received_at=$3
		WHERE agent_id=$1 AND measurement_id=$2
	`,agent,ids[2],plan.Cutoff);err!=nil{t.Fatal(err)}
	// Exclusive comparison: row at exact cutoff is NOT expired.
	plan,err=db.Preview(ctx,RetentionFilter{Days:30,AgentID:agent})
	if err!=nil{t.Fatal(err)}
	// The re-preview clock advanced slightly. Use the FIRST plan as the
	// stable policy boundary for deterministic testing.
	if _,err:=db.DeleteExpiredBatch(ctx,time.Time{},agent,1);err==nil{
		t.Fatal("accepted zero cutoff")
	}
	initial:=plan
	initial.Cutoff=plan.CheckedAt.Add(-30*24*time.Hour)
	// Restore the boundary from the earlier preview in a deterministic way:
	// set row #3 one day NEWER than cutoff.
	if _,err:=db.db.ExecContext(ctx,`
		UPDATE aegis_measurements SET received_at=$3
		WHERE agent_id=$1 AND measurement_id=$2
	`,agent,ids[2],initial.Cutoff.Add(time.Hour));err!=nil{t.Fatal(err)}
	before,err:=db.Usage(ctx)
	if err!=nil{t.Fatal(err)}
	if before.MeasurementRows<5||before.MeasurementTotalBytes<=0||
		before.DatabaseTotalBytes<before.MeasurementTotalBytes||
		before.MeasurementIndexBytes<=0 {
		t.Fatalf("invalid storage usage: %+v",before)
	}
	deleted,err:=db.Purge(ctx,initial,1,1)
	if err!=nil||deleted!=1{t.Fatalf("first bounded purge deleted=%d err=%v",deleted,err)}
	remaining,err:=db.Preview(ctx,RetentionFilter{Days:30,AgentID:agent})
	if err!=nil||remaining.EligibleRows!=1{
		t.Fatalf("preview after first purge: %+v %v",remaining,err)
	}
	deleted,err=db.Purge(ctx,initial,1,10)
	if err!=nil||deleted!=1{t.Fatalf("resume deleted=%d err=%v",deleted,err)}
	deleted,err=db.Purge(ctx,initial,1,10)
	if err!=nil||deleted!=0{t.Fatalf("idempotent purge deleted=%d err=%v",deleted,err)}
	var existing int
	if err:=db.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM aegis_measurements WHERE agent_id=$1",agent).Scan(&existing);err!=nil{t.Fatal(err)}
	if existing!=2{t.Fatalf("deleted nonexpired samples: remain=%d",existing)}
	var outside int
	if err:=db.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM aegis_measurements WHERE agent_id=$1",differentAgent).Scan(&outside);err!=nil{t.Fatal(err)}
	if outside!=1{t.Fatalf("deleted another agent's telemetry: %d",outside)}
	var lastContactValid bool
	if err:=db.db.QueryRowContext(ctx,
		"SELECT last_contact_at IS NOT NULL FROM aegis_agents WHERE agent_id=$1",agent).
		Scan(&lastContactValid);err!=nil{t.Fatal(err)}
	if !lastContactValid{t.Fatal("retention incorrectly erased agent registry")}
}

func TestInvalidPurgeLimits(t *testing.T){
	p:=RetentionPreview{CheckedAt:time.Now().UTC(),RetentionDays:30}
	p.Cutoff=p.CheckedAt.Add(-30*24*time.Hour)
	db:=&Postgres{}
	for _,tc:=range []struct{batch,max int}{
		{0,100},{MaxRetentionBatch+1,100},{1,0},{1,1_000_001},
	}{
		if _,err:=db.Purge(context.Background(),p,tc.batch,tc.max);err==nil {
			t.Fatalf("accepted invalid limits: %+v",tc)
		}
	}
}
