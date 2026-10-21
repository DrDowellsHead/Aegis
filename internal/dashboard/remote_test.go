package dashboard

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const publicOriginForTest = "https://panel.example:8443"

func remoteRequest(h http.Handler, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, publicOriginForTest+path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
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

func testRemote(t *testing.T) *Handler {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>Aegis</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	users := filepath.Join(root, "users.json")
	if err := SaveAccount(users, "operator", RoleAdmin, "admin-password-very-long", false, false); err != nil {
		t.Fatal(err)
	}
	if err := SaveAccount(users, "observer", RoleViewer, "viewer-password-very-long", false, false); err != nil {
		t.Fatal(err)
	}
	h, err := NewRemote(&stub{}, users, publicOriginForTest, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func loginRemote(t *testing.T, h *Handler, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "token": password})
	w := remoteRequest(h, "POST", "/api/web/login", string(body), publicOriginForTest, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login for %s => %d %s", username, w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly ||
		cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure HTTPS session cookie")
	}
	return cookies[0]
}

func TestRemoteDashboardRBACAndSessionRevocation(t *testing.T) {
	h := testRemote(t)
	observer := loginRemote(t, h, "observer", "viewer-password-very-long")
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	if w := remoteRequest(h, "GET", "/api/web/summary", "", "", observer); w.Code != 200 {
		t.Fatalf("viewer cannot read: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/admin/users", "", "", observer); w.Code != 403 {
		t.Fatalf("viewer has admin access: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/admin/users", "", "", admin); w.Code != 200 ||
		strings.Contains(w.Body.String(), "password_hash") ||
		!strings.Contains(w.Body.String(), "observer") {
		t.Fatalf("admin list failed or leaked hashes: %d %s", w.Code, w.Body.String())
	}
	info := remoteRequest(h, "GET", "/api/web/session", "", "", observer)
	if info.Code != 200 || !strings.Contains(info.Body.String(), `"role":"viewer"`) {
		t.Fatalf("remote role missing: %s", info.Body.String())
	}
	if w := remoteRequest(h, "POST", "/api/web/admin/sessions/revoke", `{"username":"observer"}`, "https://evil.test", admin); w.Code != 403 {
		t.Fatalf("cross-origin revoke allowed: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/admin/sessions/revoke", `{"username":"operator"}`, publicOriginForTest, observer); w.Code != 403 {
		t.Fatalf("viewer revoke allowed: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/admin/sessions/revoke", `{"username":"observer"}`, publicOriginForTest, admin); w.Code != 200 {
		t.Fatalf("admin revoke failed: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/agents", "", "", observer); w.Code != 401 {
		t.Fatalf("revoked cookie worked: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/summary", "", "", admin); w.Code != 200 {
		t.Fatalf("admin session wrongly revoked: %d", w.Code)
	}
}

func TestRemoteDashboardNeverServesPlainHTTPOrWrongHost(t *testing.T) {
	h := testRemote(t)
	r := httptest.NewRequest("GET", "http://panel.example:8443/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("plain HTTP served: %d", w.Code)
	}
	r = httptest.NewRequest("GET", "https://other.example:8443/", nil)
	r.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("arbitrary Host accepted: %d", w.Code)
	}
	for _, origin := range []string{"", "https://evil.test", "http://panel.example:8443"} {
		w = remoteRequest(h, "POST", "/api/web/login", `{"username":"operator","token":"admin-password-very-long"}`, origin, nil)
		if w.Code != 403 {
			t.Fatalf("invalid Origin %q accepted: %d", origin, w.Code)
		}
	}
}

func TestRemoteLoginInvalidIdentityAndRateLimit(t *testing.T) {
	h := testRemote(t)
	for i := 0; i < 8; i++ {
		w := remoteRequest(h, "POST", "/api/web/login", `{"username":"operator","token":"wrong-password"}`, publicOriginForTest, nil)
		if w.Code != 401 {
			t.Fatalf("failed attempt %d returned %d", i, w.Code)
		}
	}
	w := remoteRequest(h, "POST", "/api/web/login", `{"username":"operator","token":"admin-password-very-long"}`, publicOriginForTest, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit bypassed: %d", w.Code)
	}
}

func TestPublicOriginValidation(t *testing.T) {
	for _, value := range []string{"http://example.com", "https://example.com/path", "https://example.com/", "https://example.com?x=1", "https://example.com:99999", "https://user@example.com"} {
		if _, err := validatePublicOrigin(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, value := range []string{"https://example.com", "https://example.com:8443", "https://127.0.0.1:8443"} {
		if _, err := validatePublicOrigin(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
}
