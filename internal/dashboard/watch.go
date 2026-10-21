package dashboard

import (
	"context"
	"net/http"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/storage"
)

// watchStatus returns aggregate evaluator health without exposing DB errors,
// connection strings, arbitrary SQL or agent registry details.
func (h *Handler) watchStatus(w http.ResponseWriter, r *http.Request) {
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
		GetWatchStatus(context.Context) (storage.WatchStatus, error)
	})
	if !ok {
		fail(w, http.StatusServiceUnavailable, "watch_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := store.GetWatchStatus(ctx)
	if err != nil {
		h.logger.Error("watch status unavailable", "error_type", "database")
		fail(w, http.StatusServiceUnavailable, "watch_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, status)
}
