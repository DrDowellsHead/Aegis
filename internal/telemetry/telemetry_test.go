package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func sample() metrics.Snapshot {
	return metrics.Snapshot{
		SchemaVersion: 1, StartedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		DurationMS: 150, OS: "linux", Arch: "amd64",
		CPU:     &metrics.CPU{UsedPercent: 12.5},
		Network: []metrics.Interface{},
	}
}

func TestSnapshotPresenceAndValidation(t *testing.T) {
	wire, err := EncodeSnapshot(sample())
	if err != nil {
		t.Fatal(err)
	}
	if wire.Cpu == nil || wire.Memory != nil || !wire.NetworkAvailable || len(wire.Network) != 0 {
		t.Fatalf("presence lost: %+v", wire)
	}
	if err := ValidateSnapshot(wire); err != nil {
		t.Fatal(err)
	}
	wire.Cpu.UsedPercent = 101
	if err := ValidateSnapshot(wire); err == nil {
		t.Fatal("invalid percent accepted")
	}
}

func TestReceiveAuth(t *testing.T) {
	dir := t.TempDir()
	registryPath, clientPath := filepath.Join(dir, "agents.json"), filepath.Join(dir, "client.json")
	_, err := identity.Provision(registryPath, clientPath)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := identity.LoadCredentials(clientPath)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := identity.LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	receiver := &Receiver{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), registry: registry}
	wire, _ := EncodeSnapshot(sample())
	req := &pb.SendRequest{AgentId: agent.AgentID, MeasurementId: "m", Snapshot: wire}
	if _, err := receiver.Send(context.Background(), req); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing token was accepted: %v", err)
	}
	validCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+agent.Token))
	if _, err := receiver.Send(validCtx, &pb.SendRequest{AgentId: agent.AgentID, MeasurementId: "m"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid payload accepted: %v", err)
	}
	resp, err := receiver.Send(validCtx, req)
	if err != nil || !resp.GetReceived() {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	badCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+strings.Repeat("0", 64)))
	if _, err := receiver.Send(badCtx, req); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token accepted: %v", err)
	}
}

func TestTLSGRPCAndInvalidTrust(t *testing.T) {
	dir := t.TempDir()
	ca, cert, key := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := identity.GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err != nil {
		t.Fatal(err)
	}
	registryFile, clientFile := filepath.Join(dir, "agents.json"), filepath.Join(dir, "client.json")
	_, err := identity.Provision(registryFile, clientFile)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := identity.LoadCredentials(clientFile)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := identity.LoadRegistry(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := ServerCredentials(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeReceiver(ctx, listener, slog.New(slog.NewTextHandler(io.Discard, nil)), transport, registry)
	}()
	address := listener.Addr().String()
	if _, err := SendOnce(context.Background(), address, agent, ca, sample()); err != nil {
		t.Fatalf("valid TLS call failed: %v", err)
	}
	wrong := agent
	wrong.Token = strings.Repeat("0", 64)
	if _, err := SendOnce(context.Background(), address, wrong, ca, sample()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token result: %v", err)
	}
	otherCA, otherCert, otherKey := filepath.Join(dir, "otherca.crt"), filepath.Join(dir, "other.crt"), filepath.Join(dir, "other.key")
	if err := identity.GenerateDevelopmentTLS("127.0.0.1", otherCA, otherCert, otherKey); err != nil {
		t.Fatal(err)
	}
	if _, err := SendOnce(context.Background(), address, agent, otherCA, sample()); err == nil {
		t.Fatal("untrusted CA accepted")
	}
	unprotected, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer unprotected.Close()
	bounded, cancelCall := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCall()
	wire, _ := EncodeSnapshot(sample())
	if _, err := pb.NewTelemetryServiceClient(unprotected).Send(bounded, &pb.SendRequest{AgentId: agent.AgentID, MeasurementId: "m", Snapshot: wire}); err == nil {
		t.Fatal("plaintext call was accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("gRPC server did not stop")
	}
}
