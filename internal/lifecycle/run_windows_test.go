//go:build windows

package lifecycle

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestSCMStopWaitsForGracefulCancellation(t *testing.T) {
	requests := make(chan svc.ChangeRequest, 1)
	statuses := make(chan svc.Status, 10)
	finished := make(chan uint32, 1)
	stopped := make(chan struct{})
	h := &serviceHandler{run: func(ctx context.Context) int {
		<-ctx.Done()
		close(stopped)
		return 0
	}}
	go func() { _, exitCode := h.Execute(nil, requests, statuses); finished <- exitCode }()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case current := <-statuses:
			if current.State == svc.Running {
				requests <- svc.ChangeRequest{Cmd: svc.Stop}
				select {
				case <-stopped:
				case <-deadline:
					t.Fatal("stop did not cancel worker")
				}
				select {
				case code := <-finished:
					if code != 0 {
						t.Fatalf("graceful SCM exit code = %d", code)
					}
				case <-deadline:
					t.Fatal("SCM stop did not complete")
				}
				return
			}
		case <-deadline:
			t.Fatal("never entered SCM Running state")
		}
	}
}
