package history

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

const testAgentID = "123e4567-e89b-42d3-a456-426614174000"
const operatorToken = "a-very-long-independent-history-token-0123456789"

type fakeReader struct {
	calls        int
	last         storage.HistoryQuery
	returned     storage.HistoryResult
	seriesCalls  int
	lastSeries   storage.SeriesQuery
	seriesResult storage.SeriesResult
}

func (f *fakeReader) ListHistory(_ context.Context, q storage.HistoryQuery) (storage.HistoryResult, error) {
	f.calls++
	f.last = q
	return f.returned, nil
}
func testHandler(t *testing.T, reader *fakeReader) *Handler {
	t.Helper()
	h, err := NewHandler(reader, operatorToken, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func request(h http.Handler, target, method, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestAuthorizationAndValidation(t *testing.T) {
	f := &fakeReader{returned: storage.HistoryResult{Items: []storage.HistoryItem{}}}
	h := testHandler(t, f)
	good := "/api/v1/measurements?agent_id=" + testAgentID
	for _, tc := range []struct {
		url, method, token string
		status             int
	}{
		{good, "GET", "", 401},
		{good, "GET", "wrong", 401},
		{good, "POST", operatorToken, 405},
		{"/nope", "GET", operatorToken, 404},
		{"/api/v1/measurements", "GET", operatorToken, 400},
		{good + "&limit=0", "GET", operatorToken, 400},
		{good + "&limit=101", "GET", operatorToken, 400},
		{good + "&unexpected=1", "GET", operatorToken, 400},
		{good + "&limit=1&limit=2", "GET", operatorToken, 400},
		{good + "&from=not-a-time", "GET", operatorToken, 400},
		{good + "&from=2026-10-20T12:00:00Z&to=2026-10-20T12:00:00Z", "GET", operatorToken, 400},
		{good + "&cursor=invalid", "GET", operatorToken, 400},
		{good, "GET", operatorToken, 200},
	} {
		w := request(h, tc.url, tc.method, tc.token)
		if w.Code != tc.status {
			t.Errorf("%s %s got %d want %d body=%s", tc.method, tc.url, w.Code, tc.status, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("response is cacheable")
		}
	}
	if f.calls != 1 {
		t.Fatalf("unexpected database queries: %d", f.calls)
	}
}

func TestKeysetCursorRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	id := "0123456789abcdef0123456789abcdef"
	f := &fakeReader{returned: storage.HistoryResult{
		Items:   []storage.HistoryItem{{AgentID: testAgentID, MeasurementID: id, StartedAt: now}},
		HasMore: true,
	}}
	h := testHandler(t, f)
	base := "/api/v1/measurements?agent_id=" + testAgentID + "&limit=1&from=2026-10-20T00%3A00%3A00Z"
	w := request(h, base, "GET", operatorToken)
	if w.Code != 200 {
		t.Fatalf("initial %d %s", w.Code, w.Body.String())
	}
	var out response
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.HasMore || out.NextCursor == "" || len(out.Items) != 1 {
		t.Fatalf("unexpected response: %+v", out)
	}
	w = request(h, base+"&cursor="+url.QueryEscape(out.NextCursor), "GET", operatorToken)
	if w.Code != 200 || !f.last.CursorTime.Equal(now) || f.last.CursorID != id {
		t.Fatalf("cursor failed: status=%d query=%+v", w.Code, f.last)
	}
	tampered := strings.Replace(base, testAgentID, "123e4567-e89b-42d3-b456-426614174000", 1)
	if got := request(h, tampered+"&cursor="+url.QueryEscape(out.NextCursor), "GET", operatorToken); got.Code != 400 {
		t.Fatalf("cross-agent cursor accepted: %d", got.Code)
	}
	if got := request(h, base+"&to=2026-10-22T00%3A00%3A00Z&cursor="+url.QueryEscape(out.NextCursor), "GET", operatorToken); got.Code != 400 {
		t.Fatalf("cross-filter cursor accepted: %d", got.Code)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for _, bad := range []string{":8081", "0.0.0.0:8081", "192.168.1.10:8081", "example.org:8081", "localhost:8081"} {
		if RequireLoopback(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, good := range []string{"127.0.0.1:8081", "[::1]:8081"} {
		if err := RequireLoopback(good); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewHandler(&fakeReader{}, "short", slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("weak admin token accepted")
	}
}

func (f *fakeReader) ListSeries(_ context.Context, q storage.SeriesQuery) (storage.SeriesResult, error) {
	f.seriesCalls++
	f.lastSeries = q
	return f.seriesResult, nil
}

func TestSeriesAPIFiltersAndSecurity(t *testing.T) {
	f := &fakeReader{seriesResult: storage.SeriesResult{
		AgentID: testAgentID, Step: "1m0s", Points: []storage.SeriesPoint{},
	}}
	h := testHandler(t, f)
	base := "/api/v1/series?agent_id=" + testAgentID +
		"&from=2026-10-20T12%3A00%3A00Z&to=2026-10-20T12%3A05%3A00Z&step=1m"
	for _, tc := range []struct {
		target, token string
		want          int
	}{
		{base, "", 401},
		{base, "wrong", 401},
		{base + "&step=5m", operatorToken, 400},
		{base + "&step=1s", operatorToken, 400},
		{base + "&to=2026-10-20T23%3A00%3A00Z", operatorToken, 400},
		{base + "&garbage=1", operatorToken, 400},
		{base, operatorToken, 200},
		{base + "&disk_path=%2F", operatorToken, 200},
	} {
		w := request(h, tc.target, "GET", tc.token)
		if w.Code != tc.want {
			t.Fatalf("%s got %d want %d: %s", tc.target, w.Code, tc.want, w.Body.String())
		}
	}
	if f.seriesCalls != 2 || f.lastSeries.DiskPath != "/" {
		t.Fatalf("series args were incorrect: %+v calls %d", f.lastSeries, f.seriesCalls)
	}
	if request(h, base, "POST", operatorToken).Code != 405 {
		t.Fatal("series accepted POST")
	}
}
