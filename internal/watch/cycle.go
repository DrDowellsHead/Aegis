// Package watch provides a bounded, cancellable sequential scan.
// It reuses the exact rule/liveness evaluation paths used by the dashboard,
// so threshold hysteresis, cooldown and incident outbox remain authoritative.
package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

const BatchSize = 100

type Evaluator interface {
	ListAgents(context.Context, storage.AgentQuery) (storage.AgentResult, error)
	EvaluateRulesAs(context.Context, string, string) ([]storage.RuleRecord, error)
	EvaluateAgentLivenessAs(context.Context, string, time.Duration, string) (string, error)
}

type ProgressFunc func(context.Context, storage.WatchProgress) error

// Cycle visits all registered agents via UUID keyset pagination.
// Failure on one agent is counted and does not starve other agents.
// A list/page/database failure terminates the cycle to avoid reporting
// a partial fleet scan as successful. No work runs after cancellation.
func Cycle(ctx context.Context, db Evaluator, offlineAfter time.Duration, report ProgressFunc) (storage.WatchProgress, error) {
	var progress storage.WatchProgress
	if offlineAfter < 10*time.Second || offlineAfter > 24*time.Hour ||
		offlineAfter%time.Second != 0 {
		return progress, fmt.Errorf("invalid offline threshold")
	}
	var cursor string
	for {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		page, err := db.ListAgents(listCtx, storage.AgentQuery{
			Limit: BatchSize, Cursor: cursor, OfflineAfter: offlineAfter,
		})
		cancel()
		if err != nil {
			progress.Errors++
			return progress, fmt.Errorf("list agents: %w", err)
		}
		for _, agent := range page.Items {
			if err := ctx.Err(); err != nil {
				return progress, err
			}
			progress.AgentsChecked++
			ruleCtx, stop := context.WithTimeout(ctx, 12*time.Second)
			rules, ruleErr := db.EvaluateRulesAs(ruleCtx, agent.AgentID, "scheduler")
			stop()
			if ruleErr != nil {
				progress.Errors++
			} else {
				progress.RulesChecked += int64(len(rules))
			}
			if err := ctx.Err(); err != nil {
				return progress, err
			}
			liveCtx, stop := context.WithTimeout(ctx, 12*time.Second)
			_, liveErr := db.EvaluateAgentLivenessAs(liveCtx, agent.AgentID, offlineAfter, "scheduler")
			stop()
			if liveErr != nil {
				progress.Errors++
			}
			// Periodic progress updates prevent a long, healthy scan from
			// appearing stale when the fleet contains hundreds of agents.
			if report != nil && progress.AgentsChecked%4 == 0 {
				progressCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err = report(progressCtx, progress)
				stop()
				if err != nil {
					progress.Errors++
					return progress, fmt.Errorf("record watch progress: %w", err)
				}
			}
		}
		if report != nil {
			progressCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			err = report(progressCtx, progress)
			stop()
			if err != nil {
				progress.Errors++
				return progress, fmt.Errorf("record watch progress: %w", err)
			}
		}
		if !page.HasMore {
			return progress, nil
		}
		if page.NextCursor == "" || page.NextCursor == cursor ||
			len(page.Items) == 0 {
			progress.Errors++
			return progress, fmt.Errorf("invalid agent pagination response")
		}
		cursor = page.NextCursor
	}
}
