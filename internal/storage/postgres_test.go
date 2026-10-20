package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPostgresDurableInsertAndDedup(t *testing.T) {
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
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	if err := db.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	agent := "123e4567-e89b-42d3-a456-426614174000"
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(raw[:])
	sample := &pb.Snapshot{
		SchemaVersion: 1, StartedAt: timestamppb.New(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)),
		Os: "linux", Arch: "amd64", Cpu: &pb.CPU{UsedPercent: 12.5},
	}
	existed, err := db.Save(ctx, agent, id, sample)
	if err != nil || existed {
		t.Fatalf("first insert existed=%v err=%v", existed, err)
	}
	existed, err = db.Save(ctx, agent, id, sample)
	if err != nil || !existed {
		t.Fatalf("retry existed=%v err=%v", existed, err)
	}
	changed := &pb.Snapshot{
		SchemaVersion: 1, StartedAt: sample.StartedAt, Os: "linux", Arch: "amd64",
		Cpu: &pb.CPU{UsedPercent: 99.9},
	}
	_, err = db.Save(ctx, agent, id, changed)
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("ID conflict not detected: %v", err)
	}
	var count int
	if err := db.db.QueryRowContext(ctx,
		"SELECT count(*) FROM aegis_measurements WHERE agent_id=$1 AND measurement_id=$2", agent, id).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("insert duplicated: %d", count)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	existed, err = db.Save(ctx, agent, id, sample)
	if err != nil || !existed {
		t.Fatalf("post-restart retry existed=%v err=%v", existed, err)
	}
}
