package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// MeasurementStore is defined by its consumer, not the database package.
type MeasurementStore interface {
	Save(context.Context, string, string, *pb.Snapshot) (alreadyStored bool, err error)
}

// HeartbeatRecorder is a separate persistence capability. Heartbeats never
// create measurements, touch snapshot clocks or modify the disk spool.
type HeartbeatRecorder interface {
	RecordHeartbeat(context.Context, string) error
}

// Receiver authenticates before consulting the durable store.
type Receiver struct {
	pb.UnimplementedTelemetryServiceServer
	logger    *slog.Logger
	registry  identity.Registry
	store     MeasurementStore
	admission *receiverAdmission
}

func (r *Receiver) Send(ctx context.Context, req *pb.SendRequest) (*pb.SendResponse, error) {
	if req == nil {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	auth := md.Get("authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") ||
		!r.registry.Authenticate(req.GetAgentId(), strings.TrimPrefix(auth[0], "Bearer ")) {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	release, admissionErr := r.admission.takeMeasurement(ctx, req.GetAgentId())
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !validMeasurementID(req.GetMeasurementId()) {
		return nil, status.Error(codes.InvalidArgument, "measurement_id must be 32 lowercase hex digits")
	}
	if err := ValidateSnapshot(req.Snapshot); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if r.store == nil {
		return nil, status.Error(codes.Unavailable, "storage unavailable")
	}
	already, err := r.store.Save(ctx, req.AgentId, req.MeasurementId, req.Snapshot)
	if err != nil {
		if errors.Is(err, storage.ErrPayloadMismatch) {
			return nil, status.Error(codes.FailedPrecondition, "measurement ID reused with different payload")
		}
		r.logger.Error("database write failed", "error", err, "agent_id", req.AgentId)
		return nil, status.Error(codes.Unavailable, "database unavailable")
	}
	r.logger.Info("telemetry durably stored",
		"agent_id", req.AgentId, "measurement_id", req.MeasurementId, "already_stored", already)
	// Never claim durability if the INSERT was not committed and verified.
	return &pb.SendResponse{Received: true, Durable: true, AlreadyStored: already}, nil
}

// Heartbeat uses the same per-agent Bearer token and verified TLS channel as
// telemetry.Send. A successful reply means the DB UPDATE has committed.
func (r *Receiver) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	if req == nil || !identity.ValidID(req.GetAgentId()) {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	auth := md.Get("authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") ||
		!r.registry.Authenticate(req.GetAgentId(), strings.TrimPrefix(auth[0], "Bearer ")) {
		return nil, status.Error(codes.Unauthenticated, "invalid agent credentials")
	}
	release, admissionErr := r.admission.takeHeartbeat(ctx)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	store, ok := r.store.(HeartbeatRecorder)
	if !ok {
		return nil, status.Error(codes.Unavailable, "heartbeat storage unavailable")
	}
	if err := store.RecordHeartbeat(ctx, req.GetAgentId()); err != nil {
		if errors.Is(err, storage.ErrAgentNotRegistered) {
			return nil, status.Error(codes.FailedPrecondition, "agent registry not synchronized")
		}
		r.logger.Error("heartbeat database update failed", "agent_id", req.GetAgentId(), "error", err)
		return nil, status.Error(codes.Unavailable, "heartbeat database unavailable")
	}
	return &pb.HeartbeatResponse{Durable: true}, nil
}

func validMeasurementID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func ServeReceiver(ctx context.Context, listener net.Listener, logger *slog.Logger, transport credentials.TransportCredentials, registry identity.Registry, store MeasurementStore) error {
	return ServeReceiverWithLimits(ctx, listener, logger, transport, registry, store, DefaultReceiverLimits())
}

// ServeReceiverWithLimits bounds all active telemetry writes across connections,
// not just each HTTP/2 connection's grpc.MaxConcurrentStreams setting.
func ServeReceiverWithLimits(ctx context.Context, listener net.Listener, logger *slog.Logger, transport credentials.TransportCredentials, registry identity.Registry, store MeasurementStore, limits ReceiverLimits) error {
	if listener == nil || logger == nil || transport == nil || store == nil {
		if listener != nil {
			_ = listener.Close()
		}
		return fmt.Errorf("listener, logger, TLS credentials and store are required")
	}
	if err := limits.Validate(); err != nil {
		_ = listener.Close()
		return fmt.Errorf("invalid receiver resource limits: %w", err)
	}
	server := grpc.NewServer(grpc.Creds(transport), grpc.MaxRecvMsgSize(1<<20), grpc.MaxConcurrentStreams(64))
	pb.RegisterTelemetryServiceServer(server, &Receiver{logger: logger, registry: registry, store: store, admission: newReceiverAdmission(limits)})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("authenticated database-backed TLS receiver started", "address", listener.Addr().String(),
		"max_inflight_samples", limits.Measurements, "max_inflight_heartbeats", limits.Heartbeats,
		"max_per_agent_samples", limits.PerAgent)
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
