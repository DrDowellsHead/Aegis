package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type ruleStore interface {
	CreateRule(context.Context, storage.RuleInput) (storage.RuleRecord, error)
	ListRules(context.Context, string) ([]storage.RuleRecord, error)
	DeleteRule(context.Context, string) (bool, error)
	EvaluateRules(context.Context, string) ([]storage.RuleRecord, error)
}

func (h *Handler) rules(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorized(r); !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	store, ok := h.reader.(ruleStore)
	if !ok {
		fail(w, http.StatusServiceUnavailable, "rules_unavailable")
		return
	}
	if r.Method == http.MethodGet {
		if r.URL.Path != "/api/web/rules" {
			methodNotAllowed(w, "POST")
			return
		}
		q := r.URL.Query()
		if len(q) != 1 || len(q["agent_id"]) != 1 || !identity.ValidID(q.Get("agent_id")) {
			fail(w, http.StatusBadRequest, "invalid_query")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		rules, err := store.ListRules(ctx, q.Get("agent_id"))
		if err != nil {
			h.ruleError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"items": rules})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "GET, POST")
		return
	}
	if !h.originAllowed(r) {
		fail(w, http.StatusForbidden, "invalid_origin")
		return
	}
	if h.remote != nil {
		session, ok := h.remoteSession(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if session.role != RoleAdmin {
			fail(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch r.URL.Path {
	case "/api/web/rules":
		var input storage.RuleInput
		if !readRuleJSON(w, r, &input) {
			return
		}
		newRule, err := store.CreateRule(ctx, input)
		if err != nil {
			h.ruleError(w, err)
			return
		}
		h.logger.Info("threshold rule created", "id", newRule.ID, "agent_id", newRule.AgentID)
		jsonResponse(w, http.StatusCreated, newRule)
	case "/api/web/rules/delete":
		var body struct {
			ID string `json:"id"`
		}
		if !readRuleJSON(w, r, &body) {
			return
		}
		if !identity.ValidID(body.ID) {
			fail(w, http.StatusBadRequest, "invalid_rule_id")
			return
		}
		deleted, err := store.DeleteRule(ctx, body.ID)
		if err != nil {
			h.ruleError(w, err)
			return
		}
		if !deleted {
			fail(w, http.StatusNotFound, "not_found")
			return
		}
		h.logger.Info("threshold rule deleted", "id", body.ID)
		jsonResponse(w, http.StatusOK, map[string]bool{"deleted": true})
	case "/api/web/rules/evaluate":
		var body struct {
			AgentID string `json:"agent_id"`
		}
		if !readRuleJSON(w, r, &body) {
			return
		}
		if !identity.ValidID(body.AgentID) {
			fail(w, http.StatusBadRequest, "invalid_agent_id")
			return
		}
		evaluated, err := store.EvaluateRules(ctx, body.AgentID)
		if err != nil {
			h.ruleError(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"items": evaluated})
	default:
		fail(w, http.StatusNotFound, "not_found")
	}
}

func readRuleJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, "invalid_query")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func (h *Handler) ruleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrRuleAgent):
		fail(w, http.StatusNotFound, "unknown_agent")
	case errors.Is(err, storage.ErrRuleExists):
		fail(w, http.StatusConflict, "rule_exists")
	case errors.Is(err, storage.ErrRuleLimit):
		fail(w, http.StatusConflict, "rule_limit")
	default:
		// Do not send PostgreSQL errors, including DSNs, to browsers.
		h.logger.Error("threshold rule database operation failed", "error", err)
		fail(w, http.StatusServiceUnavailable, "rules_unavailable")
	}
}
