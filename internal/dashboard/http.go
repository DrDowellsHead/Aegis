// Package dashboard provides a loopback-only same-origin browser gateway.
// It NEVER forwards the privileged history bearer token to client JavaScript.
package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/history"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

const cookieName = "aegis_dashboard_session"
const sessionLifetime = 8 * time.Hour

type Reader interface {
	ListAgents(context.Context, storage.AgentQuery) (storage.AgentResult, error)
	CountAgents(context.Context, time.Duration) (storage.AgentSummary, error)
	ListSeries(context.Context, storage.SeriesQuery) (storage.SeriesResult, error)
	ListHistory(context.Context, storage.HistoryQuery) (storage.HistoryResult, error)
}

type session struct {
	expires  time.Time
	username string
	role     Role
}
type Handler struct {
	reader         Reader
	passwordHash   [sha256.Size]byte
	root           string
	logger         *slog.Logger
	mu             sync.Mutex
	sessions       map[[sha256.Size]byte]session
	failedAttempts []time.Time
	remote         *remoteAuth
}

func New(reader Reader, password, staticDir string, logger *slog.Logger) (*Handler, error) {
	if reader == nil || logger == nil {
		return nil, fmt.Errorf("dashboard requires data reader and logger")
	}
	if len(password) < 32 || len(password) > 4096 || strings.TrimSpace(password) != password {
		return nil, fmt.Errorf("AEGIS_DASHBOARD_TOKEN must contain 32..4096 non-trimmed bytes")
	}
	if staticDir == "" {
		return nil, fmt.Errorf("dashboard --static-dir is required")
	}
	absolute, err := filepath.Abs(staticDir)
	if err != nil {
		return nil, fmt.Errorf("resolve static directory: %w", err)
	}
	index, err := os.Stat(filepath.Join(absolute, "index.html"))
	if err != nil || index.IsDir() {
		return nil, fmt.Errorf("dashboard bundle missing index.html (run npm run build in web/)")
	}
	return &Handler{
		reader: reader, passwordHash: sha256.Sum256([]byte(password)), root: absolute,
		logger: logger, sessions: make(map[[sha256.Size]byte]session),
	}, nil
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code string) {
	jsonResponse(w, status, map[string]string{"error": code})
}

// Match the Host header to a numeric loopback address, protecting localhost
// browser sessions from DNS-rebinding attacks using arbitrary domain names.
func validHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	return err == nil && net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}
func validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin != "http://"+r.Host {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return true
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	if h.remote != nil {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if !h.requestHostAllowed(r) {
		fail(w, http.StatusBadRequest, "invalid_host")
		return
	}
	switch r.URL.Path {
	case "/api/web/session":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		if h.remote != nil {
			h.remoteSessionInfo(w, r)
			return
		}
		_, ok := h.authorized(r)
		jsonResponse(w, http.StatusOK, map[string]bool{"authenticated": ok})
		return
	case "/api/web/login":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if !h.originAllowed(r) {
			fail(w, http.StatusForbidden, "invalid_origin")
			return
		}
		if h.remote != nil {
			h.remoteLogin(w, r)
		} else {
			h.login(w, r)
		}
		return
	case "/api/web/logout":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if !h.originAllowed(r) {
			fail(w, http.StatusForbidden, "invalid_origin")
			return
		}
		h.logout(w, r)
		return
	case "/api/web/admin/users":
		if h.remote == nil {
			fail(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		h.remoteAdmin(w, r, false)
		return
	case "/api/web/admin/sessions/revoke":
		if h.remote == nil {
			fail(w, http.StatusNotFound, "not_found")
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		if !h.originAllowed(r) {
			fail(w, http.StatusForbidden, "invalid_origin")
			return
		}
		h.remoteAdmin(w, r, true)
		return
	case "/api/web/notifications/stats":
		h.notificationSummary(w,r)
		return
	case "/api/web/incidents", "/api/web/incidents/events", "/api/web/incidents/settings",
		"/api/web/incidents/evaluate", "/api/web/incidents/acknowledge",
		"/api/web/incidents/resolve":
		h.incidents(w, r)
		return
	case "/api/web/rules", "/api/web/rules/delete", "/api/web/rules/evaluate":
		h.rules(w, r)
		return
	case "/api/web/agents":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		if _, ok := h.authorized(r); !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.agents(w, r)
		return
	case "/api/web/series":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		if _, ok := h.authorized(r); !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.series(w, r)
		return
	case "/api/web/measurements":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		if _, ok := h.authorized(r); !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.measurements(w, r)
		return
	case "/api/web/summary":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, "GET")
			return
		}
		if _, ok := h.authorized(r); !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.summary(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	h.static(w, r)
}
func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	fail(w, http.StatusMethodNotAllowed, "method_not_allowed")
}
func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	// Browser HTML fallback is allowed ONLY for known SPA routes. FileServer
	// handles versioned Vite assets without turning arbitrary paths into HTML.
	path := r.URL.Path
	if path == "/" || path == "/agents" || path == "/rules" || path == "/incidents" || path == "/settings" ||
		strings.HasPrefix(path, "/agents/") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(h.root, "index.html"))
		return
	}
	if strings.HasPrefix(path, "/assets/") || path == "/favicon.ico" {
		http.FileServer(http.Dir(h.root)).ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}
func (h *Handler) authorized(r *http.Request) ([sha256.Size]byte, bool) {
	var empty [sha256.Size]byte
	c, err := r.Cookie(cookieName)
	if err != nil || len(c.Value) != 64 {
		return empty, false
	}
	raw, err := hex.DecodeString(c.Value)
	if err != nil || len(raw) != 32 {
		return empty, false
	}
	key := sha256.Sum256(raw)
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[key]
	if !ok || !time.Now().Before(s.expires) {
		delete(h.sessions, key)
		return empty, false
	}
	return key, true
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	// Local-only bounded login throttle; a future public gateway requires
	// per-account/rate-limit + authenticated identities, TLS and CSRF tokens.
	now := time.Now()
	h.mu.Lock()
	fresh := h.failedAttempts[:0]
	for _, at := range h.failedAttempts {
		if now.Sub(at) < time.Minute {
			fresh = append(fresh, at)
		}
	}
	h.failedAttempts = fresh
	blocked := len(fresh) >= 5
	h.mu.Unlock()
	if blocked {
		w.Header().Set("Retry-After", "60")
		fail(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var body struct {
		Token string `json:"token"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	hash := sha256.Sum256([]byte(body.Token))
	if subtle.ConstantTimeCompare(hash[:], h.passwordHash[:]) != 1 {
		h.mu.Lock()
		h.failedAttempts = append(h.failedAttempts, now)
		h.mu.Unlock()
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.logger.Error("session creation failed", "error", err)
		fail(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	key := sha256.Sum256(raw)
	h.mu.Lock()
	for k, s := range h.sessions {
		if now.After(s.expires) {
			delete(h.sessions, k)
		}
	}
	if len(h.sessions) >= 128 {
		h.mu.Unlock()
		fail(w, http.StatusTooManyRequests, "session_limit")
		return
	}
	h.sessions[key] = session{expires: now.Add(sessionLifetime)}
	h.failedAttempts = nil
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: hex.EncodeToString(raw), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds()),
		// HTTP is allowed ONLY on numeric loopback. Remote HTTPS and Secure
		// cookies require a separate production gateway design.
		Secure: false,
	})
	jsonResponse(w, http.StatusOK, map[string]bool{"authenticated": true})
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if key, ok := h.authorized(r); ok {
		h.mu.Lock()
		delete(h.sessions, key)
		h.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1,
		Secure: h.remote != nil,
	})
	jsonResponse(w, http.StatusOK, map[string]bool{"authenticated": false})
}
func parseAgentsQuery(r *http.Request) (storage.AgentQuery, error) {
	values := r.URL.Query()
	for key, v := range values {
		switch key {
		case "limit", "cursor", "offline_after":
		default:
			return storage.AgentQuery{}, fmt.Errorf("unknown query parameter")
		}
		if len(v) != 1 {
			return storage.AgentQuery{}, fmt.Errorf("duplicate query parameter")
		}
	}
	q := storage.AgentQuery{Limit: 30, Cursor: values.Get("cursor"), OfflineAfter: time.Minute}
	if v := values.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return q, fmt.Errorf("invalid limit")
		}
		q.Limit = n
	}
	if v := values.Get("offline_after"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return q, fmt.Errorf("invalid duration")
		}
		q.OfflineAfter = d
	}
	return q, storage.ValidateAgentQuery(q)
}
func parseThreshold(r *http.Request) (time.Duration, error) {
	values := r.URL.Query()
	for key, v := range values {
		if key != "offline_after" || len(v) != 1 {
			return 0, fmt.Errorf("invalid query")
		}
	}
	value := values.Get("offline_after")
	if value == "" {
		return time.Minute, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 10*time.Second || d > 24*time.Hour || d%time.Second != 0 {
		return 0, fmt.Errorf("invalid duration")
	}
	return d, nil
}
func (h *Handler) agents(w http.ResponseWriter, r *http.Request) {
	q, err := parseAgentsQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.ListAgents(r.Context(), q)
	if err != nil {
		h.logger.Error("dashboard agent list failed", "error", err)
		fail(w, http.StatusServiceUnavailable, "data_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, result)
}
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	threshold, err := parseThreshold(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.CountAgents(r.Context(), threshold)
	if err != nil {
		h.logger.Error("dashboard summary failed", "error", err)
		fail(w, http.StatusServiceUnavailable, "data_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

// Serve owns its listener, uses the same conservative HTTP timeouts as the
// existing local history service, and shuts down on context cancellation.
func Serve(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger) error {
	if listener == nil || handler == nil || logger == nil {
		return fmt.Errorf("dashboard listener, handler and logger required")
	}
	address := listener.Addr().String()
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		_ = listener.Close()
		return fmt.Errorf("dashboard must bind to numeric loopback IP")
	}
	srv := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	logger.Info("local dashboard started", "listen", address)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			_ = srv.Close()
		}
		serveErr := <-done
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return err
	}
}

func (h *Handler) series(w http.ResponseWriter, r *http.Request) {
	q, err := history.ParseSeriesRequest(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.ListSeries(r.Context(), q)
	if err != nil {
		h.logger.Error("dashboard series unavailable", "error", err)
		fail(w, http.StatusServiceUnavailable, "data_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

func (h *Handler) measurements(w http.ResponseWriter, r *http.Request) {
	q, err := history.ParseHistoryRequest(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	result, err := h.reader.ListHistory(r.Context(), q)
	if err != nil {
		h.logger.Error("dashboard history unavailable", "error", err)
		fail(w, http.StatusServiceUnavailable, "data_unavailable")
		return
	}
	page, err := history.BuildPage(q, result)
	if err != nil {
		h.logger.Error("dashboard pagination encoding failed", "error", err)
		fail(w, http.StatusInternalServerError, "data_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, page)
}

// ServeRemote runs a direct TLS server. Never accept X-Forwarded-* as a
// substitute for a real TLS connection; browsers connect directly.
func ServeRemote(ctx context.Context, listener net.Listener, handler http.Handler, logger *slog.Logger, certPath, keyPath string) error {
	if listener == nil || handler == nil || logger == nil {
		return fmt.Errorf("dashboard listener, handler and logger required")
	}
	if certPath == "" || keyPath == "" {
		_ = listener.Close()
		return fmt.Errorf("dashboard TLS certificate and key are required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("dashboard TLS configuration: %w", err)
	}
	srv := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
	}
	done := make(chan error, 1)
	go func() { done <- srv.ServeTLS(listener, "", "") }()
	logger.Info("HTTPS dashboard started", "listen", listener.Addr().String())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if err != nil {
			_ = srv.Close()
		}
		serveErr := <-done
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return err
	}
}
