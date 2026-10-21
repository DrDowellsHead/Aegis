package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DrDowellsHead/Aegis/internal/alerts"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type fakeRuleReader struct {
	*stub
	listCalls, createCalls, deleteCalls, evaluateCalls int
	fail                                               error
}

func (f *fakeRuleReader) CreateRule(_ context.Context, input storage.RuleInput) (storage.RuleRecord, error) {
	f.createCalls++
	if f.fail != nil {
		return storage.RuleRecord{}, f.fail
	}
	return storage.RuleRecord{RuleInput: input, ID: exampleAgent, LastStatus: "not_evaluated"}, nil
}
func (f *fakeRuleReader) ListRules(_ context.Context, _ string) ([]storage.RuleRecord, error) {
	f.listCalls++
	if f.fail != nil {
		return nil, f.fail
	}
	return []storage.RuleRecord{{ID: exampleAgent, RuleInput: storage.RuleInput{
		AgentID: exampleAgent, Rule: alerts.Rule{Name: "CPU", Metric: "cpu", Direction: "above",
			FireAt: 85, RecoverAt: 70, WindowSeconds: 300, MinSamples: 3, MaxAgeSeconds: 120},
	}, LastStatus: "not_evaluated"}}, nil
}
func (f *fakeRuleReader) DeleteRule(_ context.Context, _ string) (bool, error) {
	f.deleteCalls++
	if f.fail != nil {
		return false, f.fail
	}
	return true, nil
}
func (f *fakeRuleReader) EvaluateRules(ctx context.Context, agentID string) ([]storage.RuleRecord, error) {
	f.evaluateCalls++
	return f.ListRules(ctx, agentID)
}

const sampleRule = `{"agent_id":"` + exampleAgent +
	`","name":"CPU","metric":"cpu","direction":"above","fire_at":85,"recover_at":70,"window_seconds":300,"min_samples":3,"max_age_seconds":120,"disk_path":""}`

func TestRuleRoutesAuthorizationAndMutations(t *testing.T) {
	h := testRemote(t)
	fake := &fakeRuleReader{stub: &stub{}}
	h.reader = fake
	list := "/api/web/rules?agent_id=" + exampleAgent
	if w := remoteRequest(h, "GET", list, "", "", nil); w.Code != 401 || fake.listCalls != 0 {
		t.Fatalf("unauthenticated listing: %d", w.Code)
	}
	viewer := loginRemote(t, h, "observer", "viewer-password-very-long")
	admin := loginRemote(t, h, "operator", "admin-password-very-long")
	if w := remoteRequest(h, "GET", list, "", "", viewer); w.Code != 200 || fake.listCalls != 1 {
		t.Fatalf("viewer read: %d", w.Code)
	}
	for _, path := range []string{"/api/web/rules", "/api/web/rules/delete", "/api/web/rules/evaluate"} {
		w := remoteRequest(h, "POST", path, sampleRule, publicOriginForTest, viewer)
		if w.Code != 403 {
			t.Errorf("viewer mutation %s: %d", path, w.Code)
		}
	}
	if w := remoteRequest(h, "POST", "/api/web/rules", sampleRule, "https://evil.example", admin); w.Code != 403 {
		t.Fatalf("CSRF failed: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/rules", sampleRule, publicOriginForTest, admin); w.Code != 201 || fake.createCalls != 1 {
		t.Fatalf("admin create: %d %s", w.Code, w.Body.String())
	}
	if w := remoteRequest(h, "POST", "/api/web/rules/evaluate",
		`{"agent_id":"`+exampleAgent+`"}`, publicOriginForTest, admin); w.Code != 200 || fake.evaluateCalls != 1 {
		t.Fatalf("admin evaluate: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/rules/delete",
		`{"id":"`+exampleAgent+`"}`, publicOriginForTest, admin); w.Code != 200 || fake.deleteCalls != 1 {
		t.Fatalf("admin delete: %d", w.Code)
	}
	if w := remoteRequest(h, "POST", "/api/web/rules", sampleRule, publicOriginForTest, nil); w.Code != 401 {
		t.Fatalf("unauthenticated create: %d", w.Code)
	}
}

func TestRuleRoutesMalformedAndRedactedErrors(t *testing.T) {
	h, _ := testDashboard(t)
	fake := &fakeRuleReader{stub: &stub{}}
	h.reader = fake
	cookie := login(t, h)
	for _, path := range []string{
		"/api/web/rules",
		"/api/web/rules?agent_id=no",
		"/api/web/rules?agent_id=" + exampleAgent + "&agent_id=" + exampleAgent,
		"/api/web/rules?agent_id=" + exampleAgent + "&extra=1",
	} {
		if w := request(h, "GET", path, "", "", cookie); w.Code != 400 {
			t.Errorf("bad query %s: %d", path, w.Code)
		}
	}
	for _, data := range []string{`{}`, `{"unexpected":1}`, sampleRule + `{}`} {
		if w := request(h, "POST", "/api/web/rules", data, "http://127.0.0.1:8082", cookie); w.Code != 400 {
			t.Errorf("bad JSON: %d", w.Code)
		}
	}
	if fake.listCalls != 0 || fake.createCalls != 0 {
		t.Fatal("invalid query reached storage")
	}
	fake.fail = errors.New("postgres://user:secret-password@server")
	w := request(h, "GET", "/api/web/rules?agent_id="+exampleAgent, "", "", cookie)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret-password") {
		t.Fatalf("database secret leaked: %d %s", w.Code, w.Body.String())
	}
}
