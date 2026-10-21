//go:build windows

package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
)

func Run(args []string, run func(context.Context) int) int {
	isService, err := svc.IsWindowsService()
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect Windows service:", err)
		return 1
	}
	if !isService {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return run(ctx)
	}
	name, ok := ServiceName(args)
	if !ok {
		fmt.Fprintln(os.Stderr, "Windows service requires: start --config FILE --component agent|receiver")
		return 2
	}
	if err := svc.Run(name, &serviceHandler{run: run}); err != nil {
		fmt.Fprintln(os.Stderr, "Windows service manager:", err)
		return 1
	}
	return 0
}

// serviceHandler bridges Windows SCM Stop/Shutdown controls to context
// cancellation and waits for spool and gRPC shutdown before reporting stop.
type serviceHandler struct {
	run func(context.Context) int
}

func (h *serviceHandler) Execute(_ []string, changes <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending, WaitHint: 10000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	stopping := false
	checkpoint := uint32(1)
	var tick *time.Ticker
	var pulse <-chan time.Time
	defer func() {
		if tick != nil {
			tick.Stop()
		}
	}()
	for {
		select {
		case code := <-done:
			status <- svc.Status{State: svc.StopPending, WaitHint: 15000, CheckPoint: checkpoint}
			if code < 0 {
				code = 1
			}
			return false, uint32(code)
		case request, open := <-changes:
			if !open {
				changes = nil
				if !stopping {
					stopping = true
					cancel()
					status <- svc.Status{State: svc.StopPending, WaitHint: 15000, CheckPoint: checkpoint}
					tick = time.NewTicker(5 * time.Second)
					pulse = tick.C
				}
				continue
			}
			switch request.Cmd {
			case svc.Interrogate:
				if stopping {
					status <- svc.Status{State: svc.StopPending, WaitHint: 15000, CheckPoint: checkpoint}
				} else {
					status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
				}
			case svc.Stop, svc.Shutdown:
				if !stopping {
					stopping = true
					cancel()
					status <- svc.Status{State: svc.StopPending, WaitHint: 15000, CheckPoint: checkpoint}
					tick = time.NewTicker(5 * time.Second)
					pulse = tick.C
				}
			}
		case <-pulse:
			checkpoint++
			status <- svc.Status{State: svc.StopPending, WaitHint: 15000, CheckPoint: checkpoint}
		}
	}
}
