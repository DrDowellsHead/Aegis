package observability

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrometheusMetricsBoundedAndCumulative(t *testing.T) {
	r := New()
	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.BeginRPC()
			r.ObserveDatabase("save", nil, 15*time.Millisecond)
			r.EndRPC("send", "OK", 15*time.Millisecond)
		}()
	}
	wg.Wait()
	var b strings.Builder
	r.WritePrometheus(&b, func() sql.DBStats { return sql.DBStats{OpenConnections: 3, InUse: 2, WaitCount: 4} })
	raw := b.String()
	for _, s := range []string{
		"aegis_grpc_requests_total{method=\"send\",code=\"OK\"} 32",
		"aegis_grpc_request_duration_seconds_bucket{operation=\"send\",le=\"0.01\"} 0",
		"aegis_grpc_request_duration_seconds_bucket{operation=\"send\",le=\"0.025\"} 32",
		"aegis_grpc_request_duration_seconds_bucket{operation=\"send\",le=\"+Inf\"} 32",
		"aegis_grpc_inflight_requests 0",
		"aegis_db_operations_total{operation=\"save\",result=\"success\"} 32",
		"aegis_sql_pool_wait_count_total 4",
		"aegis_go_heap_alloc_bytes ",
	} {
		if !strings.Contains(raw, s) {
			t.Fatalf("metric %q missing from\n%s", s, raw)
		}
	}
	// Never use user-controlled values as Prometheus labels.
	r.BeginRPC()
	r.EndRPC("agent-secret-123", "malicious\nvalue", time.Millisecond)
	b.Reset()
	r.WritePrometheus(&b, nil)
	if strings.Contains(b.String(), "agent-secret-123") || strings.Contains(b.String(), "malicious") {
		t.Fatal("dynamic high-cardinality or unsafe label escaped metrics")
	}
	r.ObserveDatabase("save", errors.New("db offline"), time.Millisecond)
	b.Reset()
	r.WritePrometheus(&b, nil)
	if !strings.Contains(b.String(), "aegis_db_operations_total{operation=\"save\",result=\"error\"} 1") {
		t.Fatal("failed database operation not counted")
	}
}

func TestDiagnosticsAuthenticationAndLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:9001", "localhost:9001", "[::]:9001", "10.0.0.1:9001", "127.0.0.1%lo:9001"} {
		if err := RequireLoopback(addr); err == nil {
			t.Fatalf("accepted public or ambiguous address: %s", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:9001", "[::1]:9001"} {
		if err := RequireLoopback(addr); err != nil {
			t.Fatal(err)
		}
	}
	token := strings.Repeat("a", 48)
	h, err := Handler(token, New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, auth, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = remote
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	for _, route := range []string{"/metrics", "/debug/pprof/", "/debug/pprof/heap"} {
		if w := request(route, "", "127.0.0.1:12345"); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", route, w.Code)
		}
		if w := request(route, "Bearer "+token, "192.0.2.2:12345"); w.Code != http.StatusForbidden {
			t.Fatalf("remote %s: %d", route, w.Code)
		}
		if w := request(route, "Bearer "+token, "127.0.0.1:12345"); w.Code != http.StatusOK {
			t.Fatalf("authorized %s: %d", route, w.Code)
		}
	}
	if w := request("/metrics", "Bearer wrong", "127.0.0.1:12345"); w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	if w := request("/metrics", "Bearer "+token, "127.0.0.1:12345"); !strings.Contains(w.Body.String(), "aegis_go_goroutines") {
		t.Fatal("missing process metrics")
	}
	if _, err := Handler("weak", New(), nil); err == nil {
		t.Fatal("short auth token accepted")
	}
}
