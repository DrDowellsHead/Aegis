package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type incidentStore interface {
	ListIncidents(context.Context, storage.IncidentQuery) (storage.IncidentPage, error)
	ListIncidentEvents(context.Context, string) ([]storage.IncidentEvent, error)
	ChangeIncident(context.Context, string, string, string) (storage.Incident, error)
	EvaluateAgentLiveness(context.Context, string, time.Duration) (string, error)
	GetIncidentSettings(context.Context) (storage.IncidentSettings, error)
	UpdateIncidentSettings(context.Context, storage.IncidentSettings) (storage.IncidentSettings, error)
}

func incidentQuery(r *http.Request) (storage.IncidentQuery, error) {
	values := r.URL.Query()
	for name, items := range values {
		if len(items) != 1 {
			return storage.IncidentQuery{}, fmt.Errorf("duplicate query")
		}
		switch name {
		case "state", "agent_id", "cursor", "limit":
		default:
			return storage.IncidentQuery{}, fmt.Errorf("unknown query")
		}
	}
	q := storage.IncidentQuery{State: "active", Limit: 50, AgentID: values.Get("agent_id"), Cursor: values.Get("cursor")}
	if value := values.Get("state"); value != "" {
		q.State = value
	}
	if value := values.Get("limit"); value != "" {
		number, err := strconv.Atoi(value)
		if err != nil {
			return q, err
		}
		q.Limit = number
	}
	if err := storage.ValidateIncidentQuery(q); err != nil {
		return q, err
	}
	return q, nil
}

func (h *Handler) incidentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrIncidentAgent):
		fail(w, http.StatusNotFound, "unknown_agent")
	case errors.Is(err, storage.ErrIncidentNotFound):
		fail(w, http.StatusNotFound, "incident_not_found")
	case errors.Is(err, storage.ErrIncidentTransition):
		fail(w, http.StatusConflict, "stale_incident_state")
	default:
		h.logger.Error("incident operation failed", "error", err)
		fail(w, http.StatusServiceUnavailable, "incidents_unavailable")
	}
}

func (h *Handler) incidents(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorized(r); !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	store, ok := h.reader.(incidentStore)
	if !ok {
		fail(w, http.StatusServiceUnavailable, "incidents_unavailable")
		return
	}
	if r.Method == http.MethodGet {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		switch r.URL.Path {
		case "/api/web/incidents":
			q, err := incidentQuery(r)
			if err != nil {
				fail(w, http.StatusBadRequest, "invalid_query")
				return
			}
			page, err := store.ListIncidents(ctx, q)
			if err != nil {
				h.incidentError(w, err)
				return
			}
			jsonResponse(w, http.StatusOK, page)
		case "/api/web/incidents/settings":
			if r.URL.RawQuery != "" {
				fail(w, http.StatusBadRequest, "invalid_query")
				return
			}
			settings, err := store.GetIncidentSettings(ctx)
			if err != nil {
				h.incidentError(w, err)
				return
			}
			jsonResponse(w, http.StatusOK, settings)
		case "/api/web/incidents/events":
			q := r.URL.Query()
			if len(q) != 1 || len(q["id"]) != 1 || !identity.ValidID(q.Get("id")) {
				fail(w, http.StatusBadRequest, "invalid_query")
				return
			}
			events, err := store.ListIncidentEvents(ctx, q.Get("id"))
			if err != nil {
				h.incidentError(w, err)
				return
			}
			jsonResponse(w, http.StatusOK, map[string]any{"items": events})
		default:
			methodNotAllowed(w, "POST")
		}
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
	actor := "local-operator"
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
		actor = session.username
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch r.URL.Path {
	case "/api/web/incidents/settings":
		var input struct {
			CooldownSeconds      int `json:"cooldown_seconds"`
			EscalateAfterSeconds int `json:"escalate_after_seconds"`
		}
		if !readRuleJSON(w, r, &input) {
			return
		}
		if input.CooldownSeconds < 0 || input.CooldownSeconds > 86400 ||
			input.EscalateAfterSeconds < 60 || input.EscalateAfterSeconds > 604800 {
			fail(w, http.StatusBadRequest, "invalid_policy")
			return
		}
		inputSettings:=storage.IncidentSettings{
			CooldownSeconds: input.CooldownSeconds, EscalateAfterSeconds: input.EscalateAfterSeconds}
		var settings storage.IncidentSettings
		var err error
		if a,ok:=store.(interface{UpdateIncidentSettingsAs(context.Context,storage.IncidentSettings,string)(storage.IncidentSettings,error)});ok{
			settings,err=a.UpdateIncidentSettingsAs(ctx,inputSettings,actor)
		}else{settings,err=store.UpdateIncidentSettings(ctx,inputSettings)}
		if err != nil {
			h.incidentError(w, err)
			return
		}
		h.logger.Info("incident policy updated", "actor", actor, "cooldown_seconds",
			settings.CooldownSeconds, "escalate_after_seconds", settings.EscalateAfterSeconds)
		jsonResponse(w, http.StatusOK, settings)
	case "/api/web/incidents/evaluate":
		var input struct {
			AgentID      string `json:"agent_id"`
			OfflineAfter string `json:"offline_after"`
		}
		if !readRuleJSON(w, r, &input) {
			return
		}
		if !identity.ValidID(input.AgentID) {
			fail(w, http.StatusBadRequest, "invalid_agent_id")
			return
		}
		threshold := time.Minute
		if input.OfflineAfter != "" {
			var err error
			threshold, err = time.ParseDuration(input.OfflineAfter)
			if err != nil {
				fail(w, http.StatusBadRequest, "invalid_threshold")
				return
			}
		}
		if threshold < 10*time.Second || threshold > 24*time.Hour || threshold%time.Second != 0 {
			fail(w, http.StatusBadRequest, "invalid_threshold")
			return
		}
		var state string
		var err error
		if a,ok:=store.(interface{EvaluateAgentLivenessAs(context.Context,string,time.Duration,string)(string,error)});ok {
			state,err=a.EvaluateAgentLivenessAs(ctx,input.AgentID,threshold,actor)
		}else{state,err=store.EvaluateAgentLiveness(ctx,input.AgentID,threshold)}
		if err != nil {
			h.incidentError(w, err)
			return
		}
		h.logger.Info("agent liveness evaluated", "agent_id", input.AgentID, "status", state, "actor", actor)
		jsonResponse(w, http.StatusOK, map[string]string{"status": state})
	case "/api/web/incidents/acknowledge", "/api/web/incidents/resolve":
		var input struct {
			ID string `json:"id"`
		}
		if !readRuleJSON(w, r, &input) {
			return
		}
		if !identity.ValidID(input.ID) {
			fail(w, http.StatusBadRequest, "invalid_incident_id")
			return
		}
		action := "acknowledge"
		if r.URL.Path == "/api/web/incidents/resolve" {
			action = "resolve"
		}
		v, err := store.ChangeIncident(ctx, input.ID, action, actor)
		if err != nil {
			h.incidentError(w, err)
			return
		}
		h.logger.Info("incident changed", "incident_id", input.ID, "action", action, "actor", actor)
		jsonResponse(w, http.StatusOK, v)
	default:
		fail(w, http.StatusNotFound, "not_found")
	}
}
