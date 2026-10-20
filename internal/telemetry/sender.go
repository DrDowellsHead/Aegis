package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SendOnce performs one bounded local request. A successful response confirms
// volatile reception, NOT durability. Retries/identity/TLS follow in step 3.2+.
func SendOnce(ctx context.Context, address, agentID string, snapshot metrics.Snapshot) (string, error) {
	if err := RequireLoopback(address); err != nil {
		return "", err
	}
	if agentID == "" || len(agentID) > 128 {
		return "", fmt.Errorf("agent ID must be 1..128 characters")
	}
	wire, err := EncodeSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	if err := ValidateSnapshot(wire); err != nil {
		return "", err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate measurement ID: %w", err)
	}
	measurementID := hex.EncodeToString(random[:])
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", fmt.Errorf("create telemetry client: %w", err)
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := pb.NewTelemetryServiceClient(conn).Send(callCtx, &pb.SendRequest{
		AgentId: agentID, MeasurementId: measurementID, Snapshot: wire,
	})
	if err != nil {
		return "", fmt.Errorf("send telemetry: %w", err)
	}
	if !response.GetReceived() {
		return "", fmt.Errorf("server did not acknowledge reception")
	}
	return measurementID, nil
}
