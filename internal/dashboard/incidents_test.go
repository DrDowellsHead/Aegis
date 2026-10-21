package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type mockIncidentStore struct {
	*stub
	lists, acks, evaluations, events int
	actor                            string
	fail                             error
}

func (m *mockIncidentStore) GetIncidentSettings(_ context.Context) (storage.IncidentSettings, error) {
	return storage.IncidentSettings{CooldownSeconds: 300, EscalateAfterSeconds: 900, UpdatedAt: time.Now().UTC()}, nil
}
func (m *mockIncidentStore) UpdateIncidentSettings(_ context.Context, v storage.IncidentSettings) (storage.IncidentSettings, error) {
	m.actor = "policy-updated"
	if m.fail != nil {
		return storage.IncidentSettings{}, m.fail
	}
	v.UpdatedAt = time.Now().UTC()
	return v, nil
}
func (m *mockIncidentStore) ListIncidents(_ context.Context, _ storage.IncidentQuery) (storage.IncidentPage, error) {
	m.lists++
	if m.fail != nil {
		return storage.IncidentPage{}, m.fail
	}
	return storage.IncidentPage{Items: []storage.Incident{{
		ID: exampleAgent, AgentID: exampleAgent, Title: "CPU high", State: "open",
		Kind: "threshold", OpenedAt: time.Now().UTC(),
	}}}, nil
}
func (m *mockIncidentStore) ListIncidentEvents(_ context.Context, _ string) ([]storage.IncidentEvent, error) {
	m.events++
	return []storage.IncidentEvent{{Action: "opened", Actor: "system", At: time.Now().UTC()}}, nil
}
func (m *mockIncidentStore) ChangeIncident(_ context.Context, _ string, action, actor string) (storage.Incident, error) {
	m.acks++
	m.actor = actor
	if m.fail != nil {
		return storage.Incident{}, m.fail
	}
	return storage.Incident{ID: exampleAgent, AgentID: exampleAgent, Title: "CPU high",
		Kind: "threshold", State: map[string]string{"acknowledge": "acknowledged", "resolve": "resolved"}[action],
		OpenedAt: time.Now().UTC()}, nil
}
func (m *mockIncidentStore) EvaluateAgentLiveness(_ context.Context, _ string, _ time.Duration) (string, error) {
	m.evaluations++
	if m.fail != nil {
		return "", m.fail
	}
	return "offline", nil
}

func TestIncidentAPIAuthenticationRBACAndOrigin(t *testing.T) {
	h := testRemote(t)
	store := &mockIncidentStore{stub: &stub{}}
	h.reader = store
	path := "/api/web/incidents"
	if w := remoteRequest(h, "GET", path, "", "", nil); w.Code != 401 || store.lists != 0 {
		t.Fatalf("unauthorized incident read: %d", w.Code)
	}
	viewer := loginRemote(t, h, "observer", "viewer-password-very-long")
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	if w := remoteRequest(h, "GET", path, "", "", viewer); w.Code != 200 {
		t.Fatalf("viewer cannot read incidents: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", path+"?state=all&limit=50", "", "", viewer); w.Code != 200 {
		t.Fatalf("incident filter: %d", w.Code)
	}
	if w := remoteRequest(h, "GET", "/api/web/incidents/events?id="+exampleAgent, "", "", viewer); w.Code != 200 || store.events != 1 {
		t.Fatalf("viewer events: %d", w.Code)
	}
	for _, path := range []string{
		"/api/web/incidents/evaluate", "/api/web/incidents/acknowledge", "/api/web/incidents/resolve",
	} {
		if w := remoteRequest(h, "POST", path, `{"id":"`+exampleAgent+`"}`, publicOriginForTest, viewer); w.Code != 403 {
			t.Fatalf("viewer mutation %s: %d", path, w.Code)
		}
	}
	if store.acks != 0 || store.evaluations != 0 {
		t.Fatal("viewer reached mutation backend")
	}
	if w := remoteRequest(h, "POST", "/api/web/incidents/acknowledge",
		`{"id":"`+exampleAgent+`"}`, "https://attacker.example", admin); w.Code != 403 {
		t.Fatalf("CSRF protection failed: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/incidents/acknowledge",
		`{"id":"`+exampleAgent+`"}`, publicOriginForTest, admin); w.Code != 200 || store.actor != "operator" {
		t.Fatalf("admin identity missing: %d %q", w.Code, store.actor)
	}
	if w := remoteRequest(h, "POST", "/api/web/incidents/resolve",
		`{"id":"`+exampleAgent+`"}`, publicOriginForTest, admin); w.Code != 200 {
		t.Fatalf("manual resolution failed: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/incidents/evaluate",
		`{"agent_id":"`+exampleAgent+`","offline_after":"1m"}`,
		publicOriginForTest, admin); w.Code != 200 || store.evaluations != 1 {
		t.Fatalf("admin contact evaluation: %d", w.Code)
	}
}
func TestIncidentPolicyHTTPRoleAndValidation(t *testing.T) {
	h := testRemote(t)
	store := &mockIncidentStore{stub: &stub{}}
	h.reader = store
	viewer := loginRemote(t, h, "observer", "viewer-password-very-long")
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	if w := remoteRequest(h, "GET", "/api/web/incidents/settings", "", "", viewer); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"cooldown_seconds":300`) {
		t.Fatalf("viewer failed to read policy: %d %s", w.Code, w.Body.String())
	}
	valid := `{"cooldown_seconds":120,"escalate_after_seconds":600}`
	if w := remoteRequest(h, "POST", "/api/web/incidents/settings", valid, publicOriginForTest, viewer); w.Code != 403 {
		t.Fatalf("viewer changed policy: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/incidents/settings", valid, "https://evil.example", admin); w.Code != 403 {
		t.Fatalf("CSRF policy update allowed: %d", w.Code)
	}
	for _, bad := range []string{
		`{"cooldown_seconds":-1,"escalate_after_seconds":900}`,
		`{"cooldown_seconds":300,"escalate_after_seconds":10}`,
		`{"cooldown_seconds":300,"escalate_after_seconds":900,"extra":1}`,
	} {
		if w := remoteRequest(h, "POST", "/api/web/incidents/settings", bad, publicOriginForTest, admin); w.Code != 400 {
			t.Fatalf("bad policy accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if w := remoteRequest(h, "POST", "/api/web/incidents/settings", valid, publicOriginForTest, admin); w.Code != 200 ||
		store.actor != "policy-updated" {
		t.Fatalf("valid policy update: %d %s", w.Code, w.Body.String())
	}
}

func TestIncidentAPIRejectsMalformedAndRedactsDBErrors(t *testing.T) {
	h, _ := testDashboard(t)
	store := &mockIncidentStore{stub: &stub{}}
	h.reader = store
	c := login(t, h)
	for _, path := range []string{
		"/api/web/incidents?unknown=1",
		"/api/web/incidents?limit=200",
		"/api/web/incidents?state=invalid",
		"/api/web/incidents?state=all&state=active",
		"/api/web/incidents?agent_id=invalid",
		"/api/web/incidents?cursor=not-base64!",
		"/api/web/incidents/events?id=invalid",
	} {
		if w := request(h, "GET", path, "", "", c); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", path, w.Code)
		}
	}
	if store.lists != 0 || store.events != 0 {
		t.Fatal("invalid queries hit database")
	}
	for _, data := range []string{`{}`, `{"id":"invalid"}`, `{"id":"` + exampleAgent + `","bogus":true}`} {
		if w := request(h, "POST", "/api/web/incidents/acknowledge", data,
			"http://127.0.0.1:8082", c); w.Code != 400 {
			t.Fatalf("bad input: %d", w.Code)
		}
	}
	if store.acks != 0 {
		t.Fatal("invalid mutation hit database")
	}
	if w := request(h, "POST", "/api/web/incidents/evaluate",
		`{"agent_id":"`+exampleAgent+`","offline_after":"1s"}`,
		"http://127.0.0.1:8082", c); w.Code != 400 {
		t.Fatalf("bad offline threshold accepted: %d", w.Code)
	}
	store.fail = errors.New("postgres://user:secret-password@example")
	w := request(h, "GET", "/api/web/incidents", "", "", c)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret-password") {
		t.Fatalf("database details leaked: %d %s", w.Code, w.Body.String())
	}
	store.fail = storage.ErrIncidentTransition
	w = request(h, "POST", "/api/web/incidents/resolve", `{"id":"`+exampleAgent+`"}`,
		"http://127.0.0.1:8082", c)
	if w.Code != 409 {
		t.Fatalf("stale state not rejected: %d", w.Code)
	}
}
