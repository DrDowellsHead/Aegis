package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func agentUUID(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	v := hex.EncodeToString(raw[:])
	return v[:8] + "-" + v[8:12] + "-" + v[12:16] + "-" + v[16:20] + "-" + v[20:]
}

func TestAgentQueryValidation(t *testing.T) {
	q := AgentQuery{Limit: 100, OfflineAfter: time.Minute}
	if err := ValidateAgentQuery(q); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []AgentQuery{
		{Limit: 0, OfflineAfter: time.Minute},
		{Limit: 201, OfflineAfter: time.Minute},
		{Limit: 1, OfflineAfter: time.Second},
		{Limit: 1, OfflineAfter: 25 * time.Hour},
		{Limit: 1, OfflineAfter: time.Minute + time.Nanosecond},
		{Limit: 1, OfflineAfter: time.Minute, Cursor: "bad"},
	} {
		if err := ValidateAgentQuery(bad); err == nil {
			t.Fatalf("accepted invalid query: %+v", bad)
		}
	}
}

func TestRegistryAndLivenessPostgres(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AEGIS_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal("not idempotent:", err)
	}
	if err := db.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	first, second := agentUUID(t), agentUUID(t)
	if first > second {
		first, second = second, first
	}
	if err := db.SyncAgents(ctx, []string{first, second}); err != nil {
		t.Fatal(err)
	}
	q := AgentQuery{Limit: 1, OfflineAfter: time.Minute}
	page1, err := db.ListAgents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 1 || page1.Items[0].AgentID != first ||
		page1.Items[0].Status != "unknown" || page1.Items[0].LastContactAt != nil ||
		!page1.HasMore || page1.NextCursor != first {
		t.Fatalf("newly registered must be unknown, page=%+v", page1)
	}
	q.Cursor = page1.NextCursor
	page2, err := db.ListAgents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 1 || page2.Items[0].AgentID != second ||
		page2.HasMore || page2.Items[0].Status != "unknown" {
		t.Fatalf("second page incorrect: %+v", page2)
	}
	if err := db.SyncAgents(ctx, []string{"invalid"}); err == nil {
		t.Fatal("invalid sync accepted")
	}
	// The agent's reported time is old. Only DB receipt time determines online.
	id := randomMeasurementID(t)
	s := &pb.Snapshot{SchemaVersion: 1, StartedAt: timestamppb.New(
		time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
		Os: "linux", Arch: "amd64", Cpu: &pb.CPU{UsedPercent: 42},
	}
	if already, err := db.Save(ctx, first, id, s); err != nil || already {
		t.Fatalf("first contact: already=%v err=%v", already, err)
	}
	q.Cursor = ""
	list, err := db.ListAgents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	observed := list.Items[0]
	if observed.Status != "online" || observed.LastContactAt == nil ||
		observed.LastSampleAt == nil || observed.OS != "linux" || observed.Arch != "amd64" {
		t.Fatalf("telemetry contact not registered correctly: %+v", observed)
	}
	if observed.LastSampleAt.Year() != 2022 ||
		time.Since(*observed.LastContactAt) > time.Minute {
		t.Fatal("confused agent clock and server reception")
	}
	// Simulate two hours without a successful authenticated request.
	if _, err := db.db.ExecContext(ctx,
		"UPDATE aegis_agents SET last_contact_at=statement_timestamp()-interval '2 hours' WHERE agent_id=$1", first); err != nil {
		t.Fatal(err)
	}
	offline, err := db.ListAgents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if offline.Items[0].Status != "offline" {
		t.Fatalf("expected offline: %+v", offline.Items[0])
	}
	// A lost ACK retried with the SAME old measurement must update contact
	// without adding duplicate telemetry or replacing the sample time.
	if already, err := db.Save(ctx, first, id, s); err != nil || !already {
		t.Fatalf("deduplicated contact: already=%v err=%v", already, err)
	}
	online, err := db.ListAgents(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if online.Items[0].Status != "online" || online.Items[0].LastSampleAt.Year() != 2022 {
		t.Fatalf("retry did not refresh contact correctly: %+v", online.Items[0])
	}
	// Removing an ID from the authoritative JSON hides it from the list.
	if err := db.SyncAgents(ctx, []string{first}); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListAgents(ctx, AgentQuery{Limit: 100, OfflineAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 1 || all.Items[0].AgentID != first {
		t.Fatalf("removed registration still visible: %+v", all.Items)
	}
	if err := db.SyncAgents(ctx, []string{first, second}); err != nil {
		t.Fatal(err)
	}
	readded, err := db.ListAgents(ctx, AgentQuery{Limit: 100, OfflineAfter: time.Minute})
	if err != nil || len(readded.Items) != 2 {
		t.Fatalf("reconciliation not reversible: %v %+v", err, readded)
	}
}
