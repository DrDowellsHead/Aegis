// Package server owns the lifecycle of Aegis's diagnostic HTTP server.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Handler exposes process liveness, not database or agent readiness.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	return mux
}

// Serve takes ownership of listener. Cancellation drains active requests for
// at most five seconds, then closes remaining connections before returning.
func Serve(ctx context.Context, listener net.Listener, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	logger.Info("server started", "address", listener.Addr().String())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			_ = srv.Close()
		}
		serveErr := <-done
		if err != nil {
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", serveErr)
		}
		logger.Info("server stopped")
		return nil
	}
}
