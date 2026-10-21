package dashboard

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

func TestSecurityAuditIsDurableInContractAndAdminOnly(t *testing.T) {
	h := testRemote(t)
	observer := loginRemote(t, h, "observer", "viewer-password-very-long")
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	store := h.reader.(*stub)
	bad := remoteRequest(h, "POST", "/api/web/login",
		`{"username":"operator","token":"incorrect-password"}`, publicOriginForTest, nil)
	if bad.Code != http.StatusUnauthorized {
		t.Fatal(bad.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/admin/security-events", "", "", nil); w.Code != 401 {
		t.Fatalf("anonymous security audit read: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/admin/security-events", "", "", observer); w.Code != 403 {
		t.Fatalf("viewer security audit read: %d", w.Code)
	}
	page := remoteRequest(h, "GET", "/api/web/admin/security-events?action=login_ok&actor=operator&limit=20",
		"", "", admin)
	if page.Code != 200 || strings.Contains(page.Body.String(), "password") ||
		!strings.Contains(page.Body.String(), "login_ok") {
		t.Fatalf("admin security audit response: %d %s", page.Code, page.Body.String())
	}
	for _, url := range []string{
		"/api/web/admin/security-events?limit=0",
		"/api/web/admin/security-events?action=unknown",
		"/api/web/admin/security-events?actor=some%20name",
		"/api/web/admin/security-events?cursor=invalid",
		"/api/web/admin/security-events?action=login_ok&action=logout",
		"/api/web/admin/security-events?unexpected=1",
	} {
		if w := remoteRequest(h, "GET", url, "", "", admin); w.Code != 400 {
			t.Fatalf("invalid query accepted %s => %d", url, w.Code)
		}
	}
	if w := remoteRequest(h, "POST", "/api/web/admin/security-events", "{}", publicOriginForTest, admin); w.Code != 405 {
		t.Fatalf("audit endpoint is writable: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/logout", "", publicOriginForTest, observer); w.Code != 200 {
		t.Fatalf("logout failed: %d", w.Code)
	}
	store.auditMu.Lock()
	actions := map[string]int{}
	for _, e := range store.auditEvents {
		actions[e.Action]++
	}
	store.auditMu.Unlock()
	for _, action := range []string{"login_ok", "login_failed", "logout", "admin_denied"} {
		if actions[action] == 0 {
			t.Fatalf("missing security event %s: %+v", action, actions)
		}
	}
}

func TestSecurityAuditFailureClosesLoginAndAdminMutations(t *testing.T) {
	h := testRemote(t)
	store := h.reader.(*stub)
	store.auditError = errors.New("postgres password=do-not-leak")
	w := remoteRequest(h, "POST", "/api/web/login",
		`{"username":"operator","token":"admin-password-very-long"}`,
		publicOriginForTest, nil)
	if w.Code != http.StatusServiceUnavailable ||
		strings.Contains(w.Body.String(), "do-not-leak") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("unaudited login accepted or error leaked: %d %s", w.Code, w.Body.String())
	}
	if len(h.sessions) != 0 {
		t.Fatal("session persisted despite missing audit")
	}
	store.auditError = nil
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	viewer := loginRemote(t, h, "observer", "viewer-password-very-long")
	store.auditError = errors.New("database unavailable")
	w = remoteRequest(h, "POST", "/api/web/admin/sessions/revoke",
		`{"username":"observer"}`, publicOriginForTest, admin)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("mutation escaped failed audit: %d", w.Code)
	}
	if w = remoteRequest(h, "GET", "/api/web/agents", "", "", viewer); w.Code != 200 {
		t.Fatalf("viewer session revoked despite failure: %d", w.Code)
	}
	w = remoteRequest(h, "POST", "/api/web/logout", "", publicOriginForTest, viewer)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("logout accepted without audit: %d", w.Code)
	}
	if w = remoteRequest(h, "GET", "/api/web/agents", "", "", viewer); w.Code != 200 {
		t.Fatalf("logout changed in-memory session despite failed journal: %d", w.Code)
	}
	store.auditError = nil
	if w = remoteRequest(h, "POST", "/api/web/admin/sessions/revoke",
		`{"username":"observer"}`, publicOriginForTest, admin); w.Code != 200 {
		t.Fatalf("revoke after restoring audit failed: %d", w.Code)
	}
	if w = remoteRequest(h, "GET", "/api/web/agents", "", "", viewer); w.Code != 401 {
		t.Fatalf("session still authorized after successful revoke: %d", w.Code)
	}
}

func TestSecurityAuditReadFailsClosed(t *testing.T) {
	h := testRemote(t)
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	h.reader.(*stub).auditError = errors.New("postgres://user:very-secret@db")
	w := remoteRequest(h, "GET", "/api/web/admin/security-events", "", "", admin)
	if w.Code != 503 || strings.Contains(w.Body.String(), "very-secret") {
		t.Fatalf("security audit DB error leaked: %d %s", w.Code, w.Body.String())
	}
}

var _ = storage.SecurityEvent{}
