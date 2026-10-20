package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
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
	s := sample()
	wire, err := EncodeSnapshot(s)
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

func TestReceiveValidation(t *testing.T) {
	receiver := &Receiver{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := receiver.Send(context.Background(), &pb.SendRequest{AgentId: "a", MeasurementId: "m"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument; got %v", err)
	}
	wire, _ := EncodeSnapshot(sample())
	response, err := receiver.Send(context.Background(), &pb.SendRequest{AgentId: "a", MeasurementId: "m", Snapshot: wire})
	if err != nil || !response.GetReceived() {
		t.Fatalf("response=%v err=%v", response, err)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for _, bad := range []string{"0.0.0.0:9090", ":9090", "example.com:9090", "192.168.1.1:9090"} {
		if RequireLoopback(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if err := RequireLoopback("127.0.0.1:9090"); err != nil {
		t.Fatal(err)
	}
}

func TestRealGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeReceiver(ctx, listener, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()
	wire, _ := EncodeSnapshot(sample())
	response, err := pb.NewTelemetryServiceClient(conn).Send(callCtx, &pb.SendRequest{
		AgentId: "test-agent", MeasurementId: "test-measurement", Snapshot: wire,
	})
	if err != nil || !response.GetReceived() {
		t.Fatalf("response=%v err=%v", response, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "stopped") {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("receiver did not terminate")
	}
}
