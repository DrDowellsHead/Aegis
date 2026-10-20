package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Receiver validates telemetry but deliberately does not persist it in step 3.1.
type Receiver struct {
	pb.UnimplementedTelemetryServiceServer
	logger *slog.Logger
}

func (r *Receiver) Send(_ context.Context, req *pb.SendRequest) (*pb.SendResponse, error) {
	if req == nil || req.AgentId == "" || req.MeasurementId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id and measurement_id are required")
	}
	if len(req.AgentId) > 128 || len(req.MeasurementId) > 128 {
		return nil, status.Error(codes.InvalidArgument, "identifier is too long")
	}
	if err := ValidateSnapshot(req.Snapshot); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	r.logger.Info("telemetry received (not persisted)",
		"agent_id", req.AgentId,
		"measurement_id", req.MeasurementId,
		"started_at", req.Snapshot.StartedAt.AsTime(),
		"partial", len(req.Snapshot.Errors) > 0)
	return &pb.SendResponse{Received: true}, nil
}

// ServeReceiver only accepts pre-bound loopback listeners. No authentication or
// TLS is provided here; exposing this service publicly would be unsafe.
func ServeReceiver(ctx context.Context, listener net.Listener, logger *slog.Logger) error {
	if listener == nil || logger == nil {
		return fmt.Errorf("listener and logger are required")
	}
	if err := RequireLoopback(listener.Addr().String()); err != nil {
		_ = listener.Close()
		return err
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.MaxConcurrentStreams(64))
	pb.RegisterTelemetryServiceServer(server, &Receiver{logger: logger})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("telemetry receiver started", "address", listener.Addr().String())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// GracefulStop waits for in-flight RPCs, so enforce a finite upper bound.
		drained := make(chan struct{})
		go func() { server.GracefulStop(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(5 * time.Second):
			server.Stop()
			<-drained
		}
		if err := <-done; err != nil {
			return err
		}
		logger.Info("telemetry receiver stopped")
		return nil
	}
}

// RequireLoopback prevents exposing the unauthenticated phase-3.1 endpoint.
func RequireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("unauthenticated telemetry is restricted to a numeric loopback IP")
	}
	return nil
}
