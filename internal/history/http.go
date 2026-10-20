// Package history exposes a read-only, loopback-only HTTP JSON API.
// Unlike the gRPC ingestion service, it uses a separate operator token.
package history

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type Reader interface {
	ListHistory(context.Context, storage.HistoryQuery) (storage.HistoryResult, error)
	ListSeries(context.Context, storage.SeriesQuery) (storage.SeriesResult, error)
	ListAgents(context.Context, storage.AgentQuery) (storage.AgentResult, error)
}

type Handler struct {
	reader      Reader
	tokenDigest [sha256.Size]byte
	logger      *slog.Logger
}

type response struct {
	Items      []storage.HistoryItem `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

type cursorData struct {
	AgentID       string    `json:"agent_id"`
	StartedAt     time.Time `json:"started_at"`
	MeasurementID string    `json:"measurement_id"`
	From          string    `json:"from,omitempty"`
	To            string    `json:"to,omitempty"`
}

// NewHandler refuses to expose the API without a strong, separate operator
// credential. Only its SHA-256 digest remains in memory.
func NewHandler(reader Reader, operatorToken string, logger *slog.Logger) (*Handler, error) {
	if reader == nil || logger == nil || len(operatorToken) < 32 || len(operatorToken) > 4096 ||
		strings.TrimSpace(operatorToken) != operatorToken {
		return nil, fmt.Errorf("history requires a reader, logger, and operator token (32..4096 bytes)")
	}
	return &Handler{reader: reader, tokenDigest: sha256.Sum256([]byte(operatorToken)), logger: logger}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != "/api/v1/measurements" && r.URL.Path != "/api/v1/series" && r.URL.Path != "/api/v1/agents" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	digest := sha256.Sum256([]byte(strings.TrimPrefix(auth[0], "Bearer ")))
	if subtle.ConstantTimeCompare(digest[:], h.tokenDigest[:]) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.URL.Path == "/api/v1/series" {
		h.serveSeries(w, r)
		return
	}
	if r.URL.Path == "/api/v1/agents" {
		h.serveAgents(w, r)
		return
	}
	q, err := parseQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.ListHistory(r.Context(), q)
	if err != nil {
		h.logger.Error("history database query failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "history_unavailable")
		return
	}
	out := response{Items: result.Items, HasMore: result.HasMore}
	if result.HasMore && len(result.Items) > 0 {
		last := result.Items[len(result.Items)-1]
		from, to := "", ""
		if !q.From.IsZero() {
			from = q.From.Format(time.RFC3339Nano)
		}
		if !q.To.IsZero() {
			to = q.To.Format(time.RFC3339Nano)
		}
		raw, err := json.Marshal(cursorData{
			AgentID: q.AgentID, StartedAt: last.StartedAt, MeasurementID: last.MeasurementID,
			From: from, To: to,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encoding_failed")
			return
		}
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		h.logger.Error("write history response", "error", err)
	}
}

func parseQuery(r *http.Request) (storage.HistoryQuery, error) {
	values := r.URL.Query()
	for key, parts := range values {
		switch key {
		case "agent_id", "from", "to", "limit", "cursor":
		default:
			return storage.HistoryQuery{}, fmt.Errorf("unknown query parameter")
		}
		if len(parts) != 1 {
			return storage.HistoryQuery{}, fmt.Errorf("duplicate query parameter")
		}
	}
	id := values.Get("agent_id")
	if !identity.ValidID(id) {
		return storage.HistoryQuery{}, fmt.Errorf("invalid agent ID")
	}
	q := storage.HistoryQuery{AgentID: id, Limit: 50}
	if v := values.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return storage.HistoryQuery{}, fmt.Errorf("invalid limit")
		}
		q.Limit = n
	}
	var fromStr, toStr string
	if v := values.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return storage.HistoryQuery{}, fmt.Errorf("invalid from")
		}
		q.From = t.UTC()
		fromStr = q.From.Format(time.RFC3339Nano)
	}
	if v := values.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return storage.HistoryQuery{}, fmt.Errorf("invalid to")
		}
		q.To = t.UTC()
		toStr = q.To.Format(time.RFC3339Nano)
	}
	if !q.From.IsZero() && !q.To.IsZero() && !q.From.Before(q.To) {
		return storage.HistoryQuery{}, fmt.Errorf("from must precede to")
	}
	if v := values.Get("cursor"); v != "" {
		if len(v) > 1024 {
			return storage.HistoryQuery{}, fmt.Errorf("cursor too large")
		}
		raw, err := base64.RawURLEncoding.DecodeString(v)
		if err != nil {
			return storage.HistoryQuery{}, fmt.Errorf("malformed cursor")
		}
		var c cursorData
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return storage.HistoryQuery{}, fmt.Errorf("invalid cursor")
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return storage.HistoryQuery{}, fmt.Errorf("invalid cursor trailing bytes")
		}
		if c.AgentID != id || c.From != fromStr || c.To != toStr ||
			c.StartedAt.IsZero() || !validID(c.MeasurementID) {
			return storage.HistoryQuery{}, fmt.Errorf("cursor does not match filters")
		}
		q.CursorTime = c.StartedAt.UTC()
		q.CursorID = c.MeasurementID
	}
	return q, nil
}

func validID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// RequireLoopback accepts only a numeric loopback IP, not wildcard or DNS.
func RequireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("history API must listen on a numeric loopback IP")
	}
	return nil
}

// Serve starts a local-only HTTP server and stops it on context cancellation.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger) error {
	if listener == nil || handler == nil || logger == nil {
		return fmt.Errorf("listener, handler and logger required")
	}
	if err := RequireLoopback(listener.Addr().String()); err != nil {
		_ = listener.Close()
		return err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	logger.Info("local history API started", "listen", listener.Addr().String())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(drainCtx)
		if err != nil {
			_ = server.Close()
		}
		srvErr := <-done
		if srvErr != nil && !errors.Is(srvErr, http.ErrServerClosed) {
			return srvErr
		}
		return err
	}
}

func (h *Handler) serveSeries(w http.ResponseWriter, r *http.Request) {
	q, err := parseSeriesQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.ListSeries(r.Context(), q)
	if err != nil {
		h.logger.Error("series database query failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "series_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("write series response", "error", err)
	}
}

// parseSeriesQuery rejects unknown/duplicate parameters and caps both time
// range and bucket count so clients cannot request unbounded grouping.
func parseSeriesQuery(r *http.Request) (storage.SeriesQuery, error) {
	values := r.URL.Query()
	for key, parts := range values {
		switch key {
		case "agent_id", "from", "to", "step", "disk_path":
		default:
			return storage.SeriesQuery{}, fmt.Errorf("unknown query parameter")
		}
		if len(parts) != 1 {
			return storage.SeriesQuery{}, fmt.Errorf("duplicate query parameter")
		}
	}
	agentID := values.Get("agent_id")
	if !identity.ValidID(agentID) {
		return storage.SeriesQuery{}, fmt.Errorf("invalid agent")
	}
	from, err := time.Parse(time.RFC3339Nano, values.Get("from"))
	if err != nil {
		return storage.SeriesQuery{}, fmt.Errorf("invalid from")
	}
	to, err := time.Parse(time.RFC3339Nano, values.Get("to"))
	if err != nil {
		return storage.SeriesQuery{}, fmt.Errorf("invalid to")
	}
	step, err := time.ParseDuration(values.Get("step"))
	if err != nil || !storage.ValidStep(step) {
		return storage.SeriesQuery{}, fmt.Errorf("invalid step")
	}
	q := storage.SeriesQuery{
		AgentID: agentID, From: from.UTC(), To: to.UTC(),
		Step: step, DiskPath: values.Get("disk_path"),
	}
	if err := storage.ValidateSeriesQuery(q); err != nil {
		return storage.SeriesQuery{}, err
	}
	return q, nil
}

func (h *Handler) serveAgents(w http.ResponseWriter, r *http.Request) {
	q, err := parseAgentsQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.ListAgents(r.Context(), q)
	if err != nil {
		h.logger.Error("agent registry query failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "agents_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		h.logger.Error("write agent registry response", "error", err)
	}
}

// Cursor is the last UUID in ascending order, independent of wall clock.
// Statuses are evaluated at DB query time and may change between pages.
func parseAgentsQuery(r *http.Request) (storage.AgentQuery, error) {
	values := r.URL.Query()
	for key, parts := range values {
		switch key {
		case "limit", "cursor", "offline_after":
		default:
			return storage.AgentQuery{}, fmt.Errorf("unknown agent query parameter")
		}
		if len(parts) != 1 {
			return storage.AgentQuery{}, fmt.Errorf("duplicate agent query parameter")
		}
	}
	q := storage.AgentQuery{Limit: 100, OfflineAfter: time.Minute, Cursor: values.Get("cursor")}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil { return storage.AgentQuery{}, fmt.Errorf("invalid limit") }
		q.Limit = n
	}
	if raw := values.Get("offline_after"); raw != "" {
		delay, err := time.ParseDuration(raw)
		if err != nil { return storage.AgentQuery{}, fmt.Errorf("invalid offline_after") }
		q.OfflineAfter = delay
	}
	if err := storage.ValidateAgentQuery(q); err != nil {
		return storage.AgentQuery{}, err
	}
	return q, nil
}
