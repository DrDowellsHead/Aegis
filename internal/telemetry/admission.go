package telemetry

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ReceiverLimits are per receiver process, not per gRPC connection.
// A rejected sample has NOT received a durable ACK and must be retried.
type ReceiverLimits struct {
	Measurements int
	Heartbeats   int
	PerAgent     int
}

func DefaultReceiverLimits() ReceiverLimits {
	return ReceiverLimits{Measurements: 8, Heartbeats: 2, PerAgent: 2}
}

func (l ReceiverLimits) Validate() error {
	if l.Measurements < 1 || l.Measurements > 64 ||
		l.Heartbeats < 1 || l.Heartbeats > 16 ||
		l.PerAgent < 1 || l.PerAgent > 8 || l.PerAgent > l.Measurements {
		return fmt.Errorf("measurements must be 1..64; heartbeats 1..16; per-agent 1..8 and <= measurements")
	}
	return nil
}

type receiverAdmission struct {
	measurements chan struct{}
	heartbeats   chan struct{}
	maxPerAgent  int
	mu           sync.Mutex
	perAgent     map[string]int
}

func newReceiverAdmission(limits ReceiverLimits) *receiverAdmission {
	return &receiverAdmission{
		measurements: make(chan struct{}, limits.Measurements),
		heartbeats:   make(chan struct{}, limits.Heartbeats),
		maxPerAgent:  limits.PerAgent,
		perAgent:     make(map[string]int),
	}
}

func (a *receiverAdmission) takeMeasurement(ctx context.Context, agentID string) (func(), error) {
	if a == nil {
		return func() {}, nil // only directly constructed legacy test receivers
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	select {
	case a.measurements <- struct{}{}:
	default:
		return nil, status.Error(codes.ResourceExhausted, "telemetry receiver busy; retry with the same measurement ID")
	}
	a.mu.Lock()
	if a.perAgent[agentID] >= a.maxPerAgent {
		a.mu.Unlock()
		<-a.measurements
		return nil, status.Error(codes.ResourceExhausted, "agent telemetry concurrency limit reached; retry")
	}
	a.perAgent[agentID]++
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		a.perAgent[agentID]--
		if a.perAgent[agentID] == 0 {
			delete(a.perAgent, agentID)
		}
		a.mu.Unlock()
		<-a.measurements
	}, nil
}

func (a *receiverAdmission) takeHeartbeat(ctx context.Context) (func(), error) {
	if a == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	select {
	case a.heartbeats <- struct{}{}:
		return func() { <-a.heartbeats }, nil
	default:
		return nil, status.Error(codes.ResourceExhausted, "heartbeat receiver busy; retry later")
	}
}
