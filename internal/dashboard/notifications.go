package dashboard

import (
	"context"
	"net/http"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// notificationSummary intentionally reveals only aggregate statuses, never
// recipients, tokens, response bodies, or SMTP server addresses.
func (h *Handler) notificationSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if _, ok := h.authorized(r); !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, "invalid_query")
		return
	}
	store, ok := h.reader.(interface {
		NotificationStats(context.Context) (storage.NotificationStats, error)
	})
	if !ok {
		fail(w, http.StatusServiceUnavailable, "notifications_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	stats, err := store.NotificationStats(ctx)
	if err != nil {
		h.logger.Error("notification queue status unavailable", "error", err)
		fail(w, http.StatusServiceUnavailable, "notifications_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, stats)
}
