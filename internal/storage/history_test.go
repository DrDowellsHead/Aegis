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

func TestHistoryKeysetAndFilteringPostgres(t *testing.T) {
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
	if err := db.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	agent := "123e4567-e89b-42d3-a456-426614174000"
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal(err)
		}
		id := hex.EncodeToString(random[:])
		ts := start
		if i == 2 {
			ts = ts.Add(time.Hour)
		}
		s := &pb.Snapshot{
			SchemaVersion: 1, StartedAt: timestamppb.New(ts),
			Os: "linux", Arch: "amd64", DurationMs: int64(i),
			Cpu:              &pb.CPU{UsedPercent: float64(i)},
			NetworkAvailable: true, Errors: map[string]string{"disk": "unavailable"},
		}
		if _, err := db.Save(ctx, agent, id, s); err != nil {
			t.Fatal(err)
		}
	}
	q := HistoryQuery{AgentID: agent, From: start, To: start.Add(2 * time.Hour), Limit: 1}
	seen := make(map[string]bool)
	for page := 0; page < 4; page++ {
		result, err := db.ListHistory(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) == 0 {
			if page != 3 {
				t.Fatalf("ran out after %d pages", page)
			}
			break
		}
		item := result.Items[0]
		if seen[item.MeasurementID] {
			t.Fatalf("duplicated page item %s", item.MeasurementID)
		}
		seen[item.MeasurementID] = true
		if item.Snapshot.CPU == nil || item.Snapshot.Errors["disk"] != "unavailable" {
			t.Fatalf("metrics lost in history: %+v", item.Snapshot)
		}
		if item.Snapshot.Network == nil || len(item.Snapshot.Network) != 0 {
			t.Fatalf("lost network presence: %+v", item.Snapshot.Network)
		}
		if page < 2 && !result.HasMore {
			t.Fatal("lost continuation")
		}
		if page == 2 && result.HasMore {
			t.Fatal("spurious continuation")
		}
		q.CursorID = item.MeasurementID
		q.CursorTime = item.StartedAt
	}
	if len(seen) != 3 {
		t.Fatalf("expected three unique rows, got %d", len(seen))
	}
	some, err := db.ListHistory(ctx, HistoryQuery{
		AgentID: agent, From: start.Add(time.Hour), To: start.Add(2 * time.Hour), Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(some.Items) != 1 {
		t.Fatalf("time filter returned %d", len(some.Items))
	}
	missing, err := db.ListHistory(ctx, HistoryQuery{
		AgentID: "123e4567-e89b-42d3-b456-426614174000", Limit: 10,
	})
	if err != nil || len(missing.Items) != 0 {
		t.Fatalf("wrong agent results: %d %v", len(missing.Items), err)
	}
}
