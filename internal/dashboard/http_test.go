package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

const examplePassword = "test-dashboard-password-0123456789-abcdef"
const exampleAgent = "123e4567-e89b-42d3-a456-426614174000"

type stub struct {
	queries int
	stats   int
	last    storage.AgentQuery
	err     error
}

func (s *stub) ListAgents(_ context.Context, q storage.AgentQuery) (storage.AgentResult, error) {
	s.queries++
	s.last = q
	if s.err != nil {
		return storage.AgentResult{}, s.err
	}
	return storage.AgentResult{Items: []storage.AgentInfo{{
		AgentID: exampleAgent, Status: "online", RegisteredAt: time.Now().UTC(),
	}}, OfflineAfter: q.OfflineAfter.String()}, nil
}
func (s *stub) CountAgents(_ context.Context, d time.Duration) (storage.AgentSummary, error) {
	s.stats++
	if s.err != nil {
		return storage.AgentSummary{}, s.err
	}
	return storage.AgentSummary{Total: 1, Online: 1, OfflineAfter: d.String(), CheckedAt: time.Now().UTC()}, nil
}
func testDashboard(t *testing.T) (*Handler, *stub) {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "index.html"), []byte("<h1>Aegis</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	reader := &stub{}
	h, err := New(reader, examplePassword, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return h, reader
}
func request(h http.Handler, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8082"+path, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func login(t *testing.T, h *Handler) *http.Cookie {
	t.Helper()
	w := request(h, "POST", "/api/web/login", `{"token":"`+examplePassword+`"}`,
		"http://127.0.0.1:8082", nil)
	if w.Code != 200 {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("expected exactly one session cookie")
	}
	c := cookies[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge < 1 {
		t.Fatalf("unsafe session cookie: %+v", c)
	}
	return c
}

func TestDashboardAuthenticationAndHostSecurity(t *testing.T) {
	h, reader := testDashboard(t)
	if w := request(h, "GET", "/api/web/agents", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if reader.queries != 0 {
		t.Fatal("queried DB before auth")
	}
	if w := request(h, "POST", "/api/web/login", `{"token":"`+examplePassword+`"}`, "", nil); w.Code != 403 {
		t.Fatal("missing Origin accepted")
	}
	if w := request(h, "POST", "/api/web/login", `{"token":"`+examplePassword+`"}`, "http://evil.test", nil); w.Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
	if w := request(h, "POST", "/api/web/login", `{"token":"bad"}`, "http://127.0.0.1:8082", nil); w.Code != 401 {
		t.Fatal("bad password accepted")
	}
	c := login(t, h)
	if strings.Contains(c.Value, examplePassword) {
		t.Fatal("plaintext password in cookie")
	}
	w := request(h, "GET", "/api/web/session", "", "", c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatal(w.Body.String())
	}
	w = request(h, "GET", "/api/web/agents?limit=1&offline_after=5m", "", "", c)
	if w.Code != 200 || reader.queries != 1 || reader.last.Limit != 1 || reader.last.OfflineAfter != 5*time.Minute {
		t.Fatalf("agent response=%d calls=%d query=%+v", w.Code, reader.queries, reader.last)
	}
	if strings.Contains(w.Body.String(), examplePassword) {
		t.Fatal("secret in API")
	}
	var result storage.AgentResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatal(err, result)
	}
	if w := request(h, "GET", "/api/web/summary", "", "", c); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if reader.stats != 1 {
		t.Fatal("stats not read")
	}
	if w := request(h, "POST", "/api/web/logout", "", "http://evil.test", c); w.Code != 403 {
		t.Fatal("cross origin logout")
	}
	if w := request(h, "POST", "/api/web/logout", "", "http://127.0.0.1:8082", c); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/api/web/agents", "", "", c); w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
	r := httptest.NewRequest("GET", "http://evil.example/api/web/session", nil)
	invalid := httptest.NewRecorder()
	h.ServeHTTP(invalid, r)
	if invalid.Code != 400 {
		t.Fatal("DNS rebinding host was accepted")
	}
}

func TestDashboardBadRequestsAndErrors(t *testing.T) {
	h, reader := testDashboard(t)
	c := login(t, h)
	for _, url := range []string{
		"/api/web/agents?limit=0",
		"/api/web/agents?limit=201",
		"/api/web/agents?limit=5&limit=6",
		"/api/web/agents?cursor=bad",
		"/api/web/agents?offline_after=1s",
		"/api/web/agents?unknown=1",
		"/api/web/summary?unknown=1",
		"/api/web/summary?offline_after=25h",
	} {
		if w := request(h, "GET", url, "", "", c); w.Code != 400 {
			t.Errorf("%s => %d", url, w.Code)
		}
	}
	if reader.queries != 0 || reader.stats != 0 {
		t.Fatal("invalid request reached storage")
	}
	if w := request(h, "POST", "/api/web/agents", "", "", c); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/api/unknown", "", "", c); w.Code != 404 {
		t.Fatal(w.Code)
	}
	reader.err = errors.New("postgres://dbuser:secret@localhost")
	w := request(h, "GET", "/api/web/agents", "", "", c)
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("DB details exposed: %s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("security headers missing")
	}
}
func TestDashboardLoginThrottleAndStaticPages(t *testing.T) {
	h, _ := testDashboard(t)
	for i := 0; i < 5; i++ {
		w := request(h, "POST", "/api/web/login", `{"token":"wrong"}`, "http://127.0.0.1:8082", nil)
		if w.Code != 401 {
			t.Fatal("unexpected login status", w.Code)
		}
	}
	w := request(h, "POST", "/api/web/login", `{"token":"`+examplePassword+`"}`,
		"http://127.0.0.1:8082", nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("throttle not effective")
	}
	for _, path := range []string{"/", "/agents", "/agents/" + exampleAgent, "/incidents", "/settings"} {
		if w := request(h, "GET", path, "", "", nil); w.Code != 200 {
			t.Fatalf("static route %s => %d", path, w.Code)
		}
	}
	if w := request(h, "GET", "/sensitive.env", "", "", nil); w.Code != 404 {
		t.Fatal("arbitrary static path exposed")
	}
}
func TestDashboardOptionsAndListenSafety(t *testing.T) {
	h, _ := testDashboard(t)
	if _, err := New(nil, examplePassword, t.TempDir(), slog.Default()); err == nil {
		t.Fatal("nil reader")
	}
	if _, err := New(&stub{}, "short", t.TempDir(), slog.Default()); err == nil {
		t.Fatal("weak password")
	}
	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, li, h, slog.Default()); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
