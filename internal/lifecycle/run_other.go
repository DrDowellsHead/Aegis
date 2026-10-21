//go:build !windows

package lifecycle

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Run transfers SIGINT/SIGTERM cancellation to the existing CLI handlers.
func Run(_ []string, run func(context.Context) int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx)
}
