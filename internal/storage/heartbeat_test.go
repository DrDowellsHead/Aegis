package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHeartbeatPresenceAndTelemetrySeparationPostgres(t *testing.T) {
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
	if _, err := db.db.ExecContext(ctx,
		"INSERT INTO aegis_agents(agent_id,registered) VALUES($1,TRUE)", id); err != nil { t.Fatal(err) }
	defer func() { _, _ = db.db.ExecContext(context.Background(), "DELETE FROM aegis_agents WHERE agent_id=$1", id) }()

	if err := db.RecordHeartbeat(ctx, "invalid"); err == nil { t.Fatal("invalid ID accepted") }
	if err := db.RecordHeartbeat(ctx, agentUUID(t)); !errors.Is(err, ErrAgentNotRegistered) {
		t.Fatalf("unknown registration accepted: %v", err)
	}
	var initialContact, initialSample sql.NullTime
	if err := db.db.QueryRowContext(ctx,
		"SELECT last_contact_at,last_sample_at FROM aegis_agents WHERE agent_id=$1", id).
		Scan(&initialContact,&initialSample); err != nil { t.Fatal(err) }
	if initialContact.Valid || initialSample.Valid { t.Fatal("new agent must be unknown") }
	if err := db.RecordHeartbeat(ctx, id); err != nil { t.Fatal(err) }
	var contact, beat, telemetry, sample sql.NullTime
	if err := db.db.QueryRowContext(ctx,
		"SELECT last_contact_at,last_heartbeat_at,last_telemetry_at,last_sample_at FROM aegis_agents WHERE agent_id=$1", id).
		Scan(&contact,&beat,&telemetry,&sample); err != nil { t.Fatal(err) }
	if !contact.Valid || !beat.Valid || telemetry.Valid || sample.Valid {
		t.Fatalf("heartbeat manufactured telemetry: contact=%v beat=%v telemetry=%v sample=%v",contact,beat,telemetry,sample)
	}
	if time.Since(contact.Time) > time.Minute || time.Since(beat.Time)>time.Minute {
		t.Fatal("heartbeat was not timestamped by server")
	}

	var oldAt sql.NullTime
	if err := db.db.QueryRowContext(ctx,
		"UPDATE aegis_agents SET last_contact_at=clock_timestamp()-interval '2 hours', last_heartbeat_at=clock_timestamp()-interval '2 hours' WHERE agent_id=$1 RETURNING last_contact_at", id).
		Scan(&oldAt); err != nil { t.Fatal(err) }
	page, err := db.ListAgents(ctx, AgentQuery{Limit:200, OfflineAfter:time.Minute})
	if err != nil { t.Fatal(err) }
	foundOffline := false
	for _, v := range page.Items { if v.AgentID==id {
		foundOffline = v.Status=="offline" && v.LastHeartbeatAt!=nil && v.LastTelemetryAt==nil
	} }
	if !foundOffline { t.Fatal("expired heartbeat was not reported offline") }

	// A historical telemetry replay restores contact without changing the
	// original measurement time, exactly like legitimate durable deduplication.
	snapshot := &pb.Snapshot{SchemaVersion:1,StartedAt:timestamppb.New(
		time.Date(2022,1,1,0,0,0,0,time.UTC)),Os:"linux",Arch:"amd64"}
	if _, err := db.Save(ctx,id,randomMeasurementID(t),snapshot); err != nil { t.Fatal(err) }
	if err := db.db.QueryRowContext(ctx,
		"SELECT last_contact_at,last_heartbeat_at,last_telemetry_at,last_sample_at FROM aegis_agents WHERE agent_id=$1", id).
		Scan(&contact,&beat,&telemetry,&sample); err != nil { t.Fatal(err) }
	if !contact.Valid || !telemetry.Valid || !beat.Valid || !sample.Valid ||
		sample.Time.Year()!=2022 || beat.Time.After(telemetry.Time) {
		t.Fatalf("telemetry incorrectly reset heartbeat or sample: %v %v %v %v",contact,beat,telemetry,sample)
	}
	if err := db.RecordHeartbeat(ctx,id); err != nil { t.Fatal(err) }
	var laterBeat sql.NullTime
	if err := db.db.QueryRowContext(ctx,
		"SELECT last_heartbeat_at FROM aegis_agents WHERE agent_id=$1",id).Scan(&laterBeat); err!=nil{t.Fatal(err)}
	if !laterBeat.Valid || laterBeat.Time.Before(beat.Time) { t.Fatal("heartbeat timestamp moved backwards") }
	if err := db.db.QueryRowContext(ctx,
		"UPDATE aegis_agents SET registered=FALSE WHERE agent_id=$1 RETURNING last_heartbeat_at",id).Scan(&laterBeat); err!=nil{t.Fatal(err)}
	if err := db.RecordHeartbeat(ctx,id); !errors.Is(err,ErrAgentNotRegistered) { t.Fatalf("revoked agent sent heartbeat: %v",err) }
}
