package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"

	"golang.org/x/crypto/bcrypt"
)

type remoteAuth struct {
	origin       string
	accounts     map[string]Account
	dummyHash    []byte
	attemptsIP   map[string][]time.Time
	attemptsUser map[string][]time.Time
	attemptsAll  []time.Time
}

func NewRemote(reader Reader, usersFile, publicOrigin, staticDir string, logger *slog.Logger) (*Handler, error) {
	origin, err := validatePublicOrigin(publicOrigin)
	if err != nil {
		return nil, err
	}
	accounts, err := LoadAccounts(usersFile)
	if err != nil {
		return nil, err
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("invalid-account-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("initialize password verifier: %w", err)
	}
	// The local operator token is not enabled in remote mode.
	randomPassword := make([]byte, 48)
	if _, err := rand.Read(randomPassword); err != nil {
		return nil, fmt.Errorf("initialize remote dashboard: %w", err)
	}
	h, err := New(reader, hex.EncodeToString(randomPassword), staticDir, logger)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Account, len(accounts))
	for _, account := range accounts {
		byName[account.Username] = account
	}
	h.remote = &remoteAuth{
		origin: origin, accounts: byName, dummyHash: dummyHash,
		attemptsIP:   make(map[string][]time.Time),
		attemptsUser: make(map[string][]time.Time),
	}
	return h, nil
}

// Explicit HTTPS authority prevents DNS rebinding and accidental proxy
// exposure. No TLS-terminating reverse proxy is trusted by this mode.
func validatePublicOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("public origin must be https://host[:port] with no path")
	}
	if u.Hostname() == "" || strings.ContainsAny(u.Hostname(), " \t\n\r") {
		return "", fmt.Errorf("invalid public origin host")
	}
	if u.Port() != "" {
		port := u.Port()
		var n int
		if _, err := fmt.Sscanf(port, "%d", &n); err != nil || n < 1 || n > 65535 || fmt.Sprint(n) != port {
			return "", fmt.Errorf("invalid public origin port")
		}
	}
	if value != u.String() {
		return "", fmt.Errorf("public origin must be canonical and must not end in /")
	}
	return value, nil
}

func (h *Handler) requestHostAllowed(r *http.Request) bool {
	if h.remote == nil {
		return validHost(r.Host)
	}
	return r.TLS != nil && "https://"+r.Host == h.remote.origin
}

func (h *Handler) originAllowed(r *http.Request) bool {
	want := "http://" + r.Host
	if h.remote != nil {
		want = h.remote.origin
	}
	if r.Header.Get("Origin") != want {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	return true
}

func keepRecent(now time.Time, attempts []time.Time) []time.Time {
	fresh := attempts[:0]
	for _, at := range attempts {
		if now.Sub(at) < time.Minute {
			fresh = append(fresh, at)
		}
	}
	return fresh
}

func (h *Handler) allowRemoteLogin(username, remoteAddr string) bool {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || net.ParseIP(ip) == nil {
		ip = "unknown"
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.remote
	a.attemptsAll = keepRecent(now, a.attemptsAll)
	for key, values := range a.attemptsIP {
		values = keepRecent(now, values)
		if len(values) == 0 {
			delete(a.attemptsIP, key)
		} else {
			a.attemptsIP[key] = values
		}
	}
	for key, values := range a.attemptsUser {
		values = keepRecent(now, values)
		if len(values) == 0 {
			delete(a.attemptsUser, key)
		} else {
			a.attemptsUser[key] = values
		}
	}
	if len(a.attemptsAll) >= 120 || len(a.attemptsIP[ip]) >= 20 || len(a.attemptsUser[username]) >= 8 {
		return false
	}
	a.attemptsAll = append(a.attemptsAll, now)
	a.attemptsIP[ip] = append(a.attemptsIP[ip], now)
	a.attemptsUser[username] = append(a.attemptsUser[username], now)
	return true
}

func (h *Handler) remoteLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var body struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF ||
		len(body.Token) > 72 || len(body.Username) > 32 {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if !h.allowRemoteLogin(body.Username, r.RemoteAddr) {
		w.Header().Set("Retry-After", "60")
		fail(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	account, found := h.remote.accounts[body.Username]
	passwordHash := h.remote.dummyHash
	if found {
		passwordHash = []byte(account.PasswordHash)
	}
	verified := bcrypt.CompareHashAndPassword(passwordHash, []byte(body.Token)) == nil
	if !found || !verified {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.logger.Error("dashboard session entropy unavailable", "error", err)
		fail(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	now := time.Now()
	key := sha256.Sum256(raw)
	h.mu.Lock()
	for k, s := range h.sessions {
		if !now.Before(s.expires) {
			delete(h.sessions, k)
		}
	}
	if len(h.sessions) >= 128 {
		h.mu.Unlock()
		fail(w, http.StatusTooManyRequests, "session_limit")
		return
	}
	h.sessions[key] = session{
		expires: now.Add(sessionLifetime), username: account.Username, role: account.Role,
	}
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: hex.EncodeToString(raw), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionLifetime.Seconds()),
	})
	h.logger.Info("dashboard login", "username", account.Username, "role", account.Role)
	jsonResponse(w, http.StatusOK, map[string]any{"authenticated": true, "role": account.Role, "username": account.Username})
}

func (h *Handler) remoteSession(r *http.Request) (session, bool) {
	key, ok := h.authorized(r)
	if !ok {
		return session{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s, found := h.sessions[key]
	return s, found && time.Now().Before(s.expires)
}

func (h *Handler) remoteSessionInfo(w http.ResponseWriter, r *http.Request) {
	s, ok := h.remoteSession(r)
	if !ok {
		jsonResponse(w, http.StatusOK, map[string]any{"authenticated": false, "mode": "remote"})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"authenticated": true, "mode": "remote", "username": s.username, "role": s.role,
	})
}

func (h *Handler) remoteAdmin(w http.ResponseWriter, r *http.Request, revoke bool) {
	s, ok := h.remoteSession(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.role != RoleAdmin {
		fail(w, http.StatusForbidden, "forbidden")
		return
	}
	if !revoke {
		users := make([]map[string]any, 0, len(h.remote.accounts))
		for _, u := range h.remote.accounts {
			users = append(users, map[string]any{"username": u.Username, "role": u.Role})
		}
		// Ordering helps operators compare unchanged user lists.
		sortAccounts(users)
		jsonResponse(w, http.StatusOK, map[string]any{"items": users})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Username string `json:"username"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF || !validUsername(body.Username) {
		fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, exists := h.remote.accounts[body.Username]; !exists {
		fail(w, http.StatusNotFound, "not_found")
		return
	}
	h.mu.Lock()
	count := 0
	for key, value := range h.sessions {
		if value.username == body.Username {
			delete(h.sessions, key)
			count++
		}
	}
	h.mu.Unlock()
	h.logger.Info("dashboard sessions revoked", "by", s.username, "username", body.Username, "count", count)
	jsonResponse(w, http.StatusOK, map[string]any{"revoked": count})
}

func sortAccounts(users []map[string]any) {
	for i := 1; i < len(users); i++ {
		for j := i; j > 0 && users[j]["username"].(string) < users[j-1]["username"].(string); j-- {
			users[j], users[j-1] = users[j-1], users[j]
		}
	}
}

// CheckServerCertificate verifies a usable TLS server leaf and its SAN.
// The trust chain is optional for public-CA deployments; use
// CheckServerCertificateWithCA to explicitly pin a CA bundle.
func CheckServerCertificate(certPath, keyPath, publicOrigin string) error {
	return CheckServerCertificateWithCA(certPath,keyPath,"",publicOrigin)
}
func CheckServerCertificateWithCA(certPath,keyPath,caPath,publicOrigin string) error {
	if _,err:=validatePublicOrigin(publicOrigin);err!=nil{return err}
	u,_:=url.Parse(publicOrigin)
	_,err:=identity.ValidateServerCertificate(certPath,keyPath,caPath,u.Hostname(),0)
	return err
}
