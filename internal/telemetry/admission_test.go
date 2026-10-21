package telemetry

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type blockedReceiverStore struct {
	entered    chan struct{}
	release    chan struct{}
	heartbeats atomic.Int32
}

func (s *blockedReceiverStore) Save(ctx context.Context, _, _ string, _ *pb.Snapshot) (bool, error) {
	select {
	case s.entered <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case <-s.release:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *blockedReceiverStore) RecordHeartbeat(context.Context, string) error {
	s.heartbeats.Add(1)
	return nil
}

func TestReceiverLimitsValidation(t *testing.T) {
	if err := DefaultReceiverLimits().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, limits := range []ReceiverLimits{
		{}, {Measurements: 65, Heartbeats: 2, PerAgent: 1},
		{Measurements: 1, Heartbeats: 0, PerAgent: 1},
		{Measurements: 1, Heartbeats: 1, PerAgent: 2},
	} {
		if err := limits.Validate(); err == nil {
			t.Fatalf("accepted invalid limits: %+v", limits)
		}
	}
}

func TestReceiverBusyRejectsWithoutSavingAndPreservesHeartbeat(t *testing.T) {
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "registry.json")
	credsPath := filepath.Join(dir, "credentials.json")
	if _, err := identity.Provision(registryPath, credsPath); err != nil {
		t.Fatal(err)
	}
	creds, err := identity.LoadCredentials(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := identity.LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	store := &blockedReceiverStore{
		entered: make(chan struct{}, 3),
		release: make(chan struct{}),
	}
	receiver := &Receiver{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry: registry, store: store,
		admission: newReceiverAdmission(ReceiverLimits{Measurements: 1, Heartbeats: 1, PerAgent: 1}),
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+creds.Token))
	wire, err := EncodeSnapshot(sample())
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.SendRequest{AgentId: creds.AgentID, MeasurementId: "0123456789abcdef0123456789abcdef", Snapshot: wire}
	done := make(chan error, 1)
	go func() {
		_, err := receiver.Send(ctx, req)
		done <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first write did not begin")
	}
	if _, err := receiver.Send(ctx, req); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted: %v", err)
	}
	select {
	case <-store.entered:
		t.Fatal("rejected request reached store")
	default:
	}
	hb, err := receiver.Heartbeat(ctx, &pb.HeartbeatRequest{AgentId: creds.AgentID})
	if err != nil || !hb.GetDurable() || store.heartbeats.Load() != 1 {
		t.Fatalf("heartbeat starved by telemetry: resp=%v err=%v", hb, err)
	}
	close(store.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked write did not finish")
	}
	if _, err := receiver.Send(ctx, req); err != nil {
		t.Fatalf("capacity was not released: %v", err)
	}
}

func TestReceiverCanceledAdmission(t *testing.T) {
	a := newReceiverAdmission(DefaultReceiverLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.takeMeasurement(ctx, "agent"); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled request accepted: %v", err)
	}
	if _, err := a.takeHeartbeat(ctx); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled heartbeat accepted: %v", err)
	}
}

// Test two independent limits: the global pool must not let one agent
// monopolize all writers, and busy telemetry must not starve heartbeats.
func TestReceiverAdmissionPerAgentFairnessAndPermitRelease(t *testing.T) {
	a := newReceiverAdmission(ReceiverLimits{
		Measurements: 2,
		PerAgent:     1,
		Heartbeats:   1,
	})
	ctx := context.Background()
	first, err := a.takeMeasurement(ctx, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	// One agent cannot use the spare global slot twice.
	if release, err := a.takeMeasurement(ctx, "agent-a"); status.Code(err) != codes.ResourceExhausted {
		if release != nil {
			release()
		}
		t.Fatalf("same agent acquired a second permit: %v", err)
	}
	second, err := a.takeMeasurement(ctx, "agent-b")
	if err != nil {
		first()
		t.Fatal(err)
	}
	// Global saturation rejects new agents even if their per-agent quota is unused.
	if release, err := a.takeMeasurement(ctx, "agent-c"); status.Code(err) != codes.ResourceExhausted {
		if release != nil {
			release()
		}
		t.Fatalf("global quota exceeded: %v", err)
	}
	heartbeatRelease, err := a.takeHeartbeat(ctx)
	if err != nil {
		t.Fatalf("telemetry starved heartbeat: %v", err)
	}
	if release, err := a.takeHeartbeat(ctx); status.Code(err) != codes.ResourceExhausted {
		if release != nil {
			release()
		}
		t.Fatalf("heartbeat quota exceeded: %v", err)
	}
	heartbeatRelease()
	// Freeing an agent's slot must free both their per-agent quota and a
	// global slot, without affecting an unrelated in-flight agent.
	first()
	newFirst, err := a.takeMeasurement(ctx, "agent-a")
	if err != nil {
		second()
		t.Fatalf("agent quota was not released: %v", err)
	}
	newFirst()
	second()
	final, err := a.takeMeasurement(ctx, "agent-c")
	if err != nil {
		t.Fatalf("global slot was not released: %v", err)
	}
	final()
}
