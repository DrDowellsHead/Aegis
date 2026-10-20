package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/healthz", 200}, {"POST", "/healthz", 405}, {"GET", "/missing", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.code {
				t.Fatalf("status=%d, want %d", w.Code, tc.code)
			}
			if tc.code == 200 && w.Body.String() != "{\"status\":\"ok\"}\n" {
				t.Fatalf("body=%q", w.Body.String())
			}
		})
	}
}

func TestServeCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, listener, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("server did not stop")
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("listener remains open")
	}
}
