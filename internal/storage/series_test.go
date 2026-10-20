package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"testing"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSeriesQueryValidation(t *testing.T) {
	id := "123e4567-e89b-42d3-a456-426614174000"
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	q := SeriesQuery{AgentID: id, From: start, To: start.Add(10 * time.Minute), Step: time.Minute}
	if err := ValidateSeriesQuery(q); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SeriesQuery{
		{AgentID: "bad", From: q.From, To: q.To, Step: q.Step},
		{AgentID: id, From: start, To: start, Step: q.Step},
		{AgentID: id, From: start, To: start.Add(600 * time.Minute), Step: q.Step},
		{AgentID: id, From: start, To: q.To, Step: time.Second},
	} {
		if err := ValidateSeriesQuery(bad); err == nil {
			t.Fatalf("accepted invalid query: %+v", bad)
		}
	}
}

func TestProjectionPartialAndNaN(t *testing.T) {
	cpu, mem, disk, path, err := project(&pb.Snapshot{})
	if err != nil || cpu != nil || mem != nil || disk != nil || path != nil {
		t.Fatal("absence was not preserved")
	}
	_, _, _, _, err = project(&pb.Snapshot{Cpu: &pb.CPU{UsedPercent: math.NaN()}})
	if err == nil {
		t.Fatal("accepted NaN")
	}
	cpu, _, _, _, err = project(&pb.Snapshot{Cpu: &pb.CPU{UsedPercent: 0}})
	if err != nil || cpu == nil {
		t.Fatal("zero misread as missing")
	}
}

func randomMeasurementID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func TestSeriesProjectionAndBackfillPostgres(t *testing.T) {
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
	// Every integration test uses an independent random UUIDv4.
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	uuid := hex.EncodeToString(raw[:])
	agent := uuid[:8] + "-" + uuid[8:12] + "-" + uuid[12:16] + "-" + uuid[16:20] + "-" + uuid[20:]
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	makeSnapshot := func(minute int, cpu *pb.CPU, path string) *pb.Snapshot {
		s := &pb.Snapshot{
			SchemaVersion: 1, StartedAt: timestamppb.New(base.Add(time.Duration(minute) * time.Minute)),
			Os: "linux", Arch: "amd64", Cpu: cpu,
		}
		if path != "" {
			s.Disk = &pb.Disk{Path: path, UsedPercent: 60}
		}
		return s
	}
	// Same minute: CPU 20 and 40 => avg 30. Third sample CPU missing.
	for _, s := range []*pb.Snapshot{
		makeSnapshot(0, &pb.CPU{UsedPercent: 20}, "/"),
		makeSnapshot(0, &pb.CPU{UsedPercent: 40}, "/"),
		makeSnapshot(0, nil, "/other"),
	} {
		if _, err := db.Save(ctx, agent, randomMeasurementID(t), s); err != nil {
			t.Fatal(err)
		}
	}

	legacy := makeSnapshot(1, &pb.CPU{UsedPercent: 80}, "/")
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	legacyID := randomMeasurementID(t)
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO aegis_measurements
		(agent_id,measurement_id,schema_version,started_at,payload,payload_sha256)
		VALUES($1,$2,1,$3,$4,$5)
	`, agent, legacyID, base.Add(time.Minute), payload, sum[:]); err != nil {
		t.Fatal(err)
	}
	query := SeriesQuery{AgentID: agent, From: base, To: base.Add(3 * time.Minute),
		Step: time.Minute, DiskPath: "/"}
	before, err := db.ListSeries(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Points) != 1 || before.Points[0].Samples != 3 ||
		before.Points[0].CPUSamples != 2 || before.Points[0].CPUAvg == nil ||
		*before.Points[0].CPUAvg != 30 || before.Points[0].DiskSamples != 2 {
		t.Fatalf("bad aggregate before backfill: %+v", before.Points)
	}
	n, err := db.BackfillBatch(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatal("legacy row was not backfilled")
	}
	n, err = db.BackfillBatch(ctx, 100)
	if err != nil || n != 0 {
		t.Fatalf("backfill not idempotent: %d %v", n, err)
	}
	after, err := db.ListSeries(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Points) != 2 || after.Points[1].CPUAvg == nil ||
		*after.Points[1].CPUAvg != 80 {
		t.Fatalf("backfilled data absent: %+v", after.Points)
	}
	if after.Points[0].DiskAvg == nil || *after.Points[0].DiskAvg != 60 {
		t.Fatalf("disk path group wrong: %+v", after.Points[0])
	}
	query.DiskPath = "/other"
	other, err := db.ListSeries(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if other.Points[0].DiskSamples != 1 {
		t.Fatal("disk path mixed")
	}
	query.DiskPath = ""
	unfiltered, err := db.ListSeries(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if unfiltered.Points[0].DiskAvg != nil {
		t.Fatal("unselected disks were averaged")
	}
}
