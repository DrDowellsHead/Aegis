package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

type securityAuditStore interface {
	RecordSecurityEvent(context.Context, storage.SecurityEventInput) error
	ListSecurityEvents(context.Context, storage.SecurityEventQuery) (storage.SecurityEventPage, error)
}

// The security audit is required for remote mode, not optional logging.
// A failed write blocks authentication or session revocation rather than
// permitting an unrecorded privileged action.
func (h *Handler) securityEvent(ctx context.Context, event storage.SecurityEventInput) bool {
	if h == nil || h.remote == nil || h.remote.audit == nil {
		return false
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.remote.audit.RecordSecurityEvent(writeCtx, event); err != nil {
		h.logger.Error("dashboard security audit unavailable", "event", event.Action, "error_type", "storage")
		return false
	}
	return true
}

func (h *Handler) securityEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if h.remote == nil {
		fail(w, http.StatusNotFound, "not_found")
		return
	}
	session, ok := h.remoteSession(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if session.role != RoleAdmin {
		if !h.securityEvent(r.Context(), storage.SecurityEventInput{
			Action: "admin_denied", Actor: session.username, Subject: "security_events", Role: string(session.role),
		}) {
			fail(w, http.StatusServiceUnavailable, "audit_unavailable")
			return
		}
		fail(w, http.StatusForbidden, "forbidden")
		return
	}
	values := r.URL.Query()
	for name, items := range values {
		if len(items) != 1 || (name != "action" && name != "actor" && name != "cursor" && name != "limit") {
			fail(w, http.StatusBadRequest, "invalid_query")
			return
		}
	}
	q := storage.SecurityEventQuery{Action: values.Get("action"), Actor: values.Get("actor"),
		Cursor: values.Get("cursor"), Limit: 50}
	if values.Has("limit") {
		value, err := strconv.Atoi(values.Get("limit"))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid_query")
			return
		}
		q.Limit = value
	}
	if err := storage.ValidateSecurityQuery(q); err != nil {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	page, err := h.remote.audit.ListSecurityEvents(ctx, q)
	if err != nil {
		h.logger.Error("dashboard security audit read unavailable", "error_type", "storage")
		fail(w, http.StatusServiceUnavailable, "audit_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, page)
}
