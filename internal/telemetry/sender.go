package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc"
)

// SendOnce sends one sample under TLS using stable provisioned credentials.
// Reception is volatile in step 3.2; persistent queue/retry follows in 3.3.
func SendOnce(ctx context.Context, address string, agent identity.Credentials, caPath string, snapshot metrics.Snapshot) (string, error) {
	if agent.AgentID == "" || agent.Token == "" {
		return "", fmt.Errorf("provisioned agent credentials required")
	}
	tlsCredentials, err := ClientCredentials(caPath)
	if err != nil {
		return "", err
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
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(tlsCredentials),
		grpc.WithPerRPCCredentials(bearer{token: agent.Token}),
	)
	if err != nil {
		return "", fmt.Errorf("create TLS telemetry client: %w", err)
	}
	defer conn.Close()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := pb.NewTelemetryServiceClient(conn).Send(callCtx, &pb.SendRequest{
		AgentId: agent.AgentID, MeasurementId: measurementID, Snapshot: wire,
	})
	if err != nil {
		return "", fmt.Errorf("send telemetry: %w", err)
	}
	if !response.GetReceived() {
		return "", fmt.Errorf("server did not acknowledge reception")
	}
	return measurementID, nil
}
