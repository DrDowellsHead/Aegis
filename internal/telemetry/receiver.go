package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Receiver authenticates the caller before validating the measurement.
// The registry is a snapshot: restart the receiver to load new provisions.
type Receiver struct {
	pb.UnimplementedTelemetryServiceServer
	logger   *slog.Logger
	registry identity.Registry
}

func (r *Receiver) Send(ctx context.Context, req *pb.SendRequest) (*pb.SendResponse, error) {
	// Never disclose whether an ID or a token was incorrect.
	if req == nil {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	authorization := md.Get("authorization")
	if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "Bearer ") ||
		!r.registry.Authenticate(req.GetAgentId(), strings.TrimPrefix(authorization[0], "Bearer ")) {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	if req.GetMeasurementId() == "" || len(req.GetMeasurementId()) > 128 {
		return nil, status.Error(codes.InvalidArgument, "invalid measurement_id")
	}
	if err := ValidateSnapshot(req.Snapshot); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	r.logger.Info("authenticated telemetry received (not persisted)",
		"agent_id", req.AgentId,
		"measurement_id", req.MeasurementId,
		"started_at", req.Snapshot.StartedAt.AsTime(),
		"partial", len(req.Snapshot.Errors) > 0)
	return &pb.SendResponse{Received: true}, nil
}

// ServeReceiver requires TLS and an authenticated registry; callers choose
// the listener's interface (including public ones) explicitly.
func ServeReceiver(ctx context.Context, listener net.Listener, logger *slog.Logger, transport credentials.TransportCredentials, registry identity.Registry) error {
	if listener == nil || logger == nil || transport == nil {
		if listener != nil {
			_ = listener.Close()
		}
		return fmt.Errorf("listener, logger and TLS credentials are required")
	}
	server := grpc.NewServer(
		grpc.Creds(transport),
		grpc.MaxRecvMsgSize(1<<20),
		grpc.MaxConcurrentStreams(64),
	)
	pb.RegisterTelemetryServiceServer(server, &Receiver{logger: logger, registry: registry})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("authenticated TLS telemetry receiver started", "address", listener.Addr().String())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
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
