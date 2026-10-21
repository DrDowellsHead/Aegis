package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// activity is read-only for admin and viewer. Unlike the mutation handlers it
// accepts NO user-provided actor identity and never reveals secrets or tokens.
func (h *Handler) activity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if _, ok := h.authorized(r); !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	store, ok := h.reader.(interface {
		ListActivity(context.Context, storage.ActivityQuery) (storage.ActivityPage, error)
	})
	if !ok {
		fail(w, http.StatusServiceUnavailable, "activity_unavailable")
		return
	}
	vals := r.URL.Query()
	allowed := map[string]bool{"category": true, "action": true, "actor": true,
		"agent_id": true, "incident_id": true, "search": true,
		"from": true, "to": true, "cursor": true, "limit": true}
	for key, items := range vals {
		if !allowed[key] || len(items) != 1 {
			fail(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	q := storage.ActivityQuery{
		Category: vals.Get("category"), Action: vals.Get("action"),
		Actor: vals.Get("actor"), AgentID: vals.Get("agent_id"),
		IncidentID: vals.Get("incident_id"), Search: vals.Get("search"),
		From: vals.Get("from"), To: vals.Get("to"), Cursor: vals.Get("cursor"),
		Limit: 50,
	}
	if vals.Has("limit") {
		n, err := strconv.Atoi(vals.Get("limit"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_query")
			return
		}
		q.Limit = n
	}
	if err := storage.ValidateActivityQuery(q); err != nil {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	page, err := store.ListActivity(ctx, q)
	if err != nil {
		h.logger.Error("activity query failed", "error", err)
		fail(w, http.StatusServiceUnavailable, "activity_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, page)
}
