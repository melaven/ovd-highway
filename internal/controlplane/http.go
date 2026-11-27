package controlplane

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"ovd/internal/api"
	"ovd/internal/pipeline/dlq"
)

// DLQAdmin exposes dead-letter queue inspection and manual retry to the
// Control Plane. *dlq.PostgresRetryStore satisfies it.
type DLQAdmin interface {
	List(ctx context.Context, limit int) ([]dlq.DLQItem, error)
	Count(ctx context.Context) (int, error)
	RetryAll(ctx context.Context) (int64, error)
}

// Handler exposes the Control Plane REST contract under /api/v1/*.
// It is intended to be mounted behind api.BearerAuth and api.CORS.
type Handler struct {
	repo     SourceRepo
	leads    LeadRepo
	settings *SettingsStore
	manager  *SourceManager
	dlq      DLQAdmin
	log      *slog.Logger
}

func NewHandler(repo SourceRepo, leads LeadRepo, settings *SettingsStore, manager *SourceManager, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		repo:     repo,
		leads:    leads,
		settings: settings,
		manager:  manager,
		log:      log.With("component", "controlplane_api"),
	}
}

// SetDLQAdmin wires the dead-letter queue store. When unset, the /dlq
// endpoints answer 503.
func (h *Handler) SetDLQAdmin(d DLQAdmin) {
	h.dlq = d
}

// Routes returns a mux with the full /api/v1 route table.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sources", h.listSources)
	mux.HandleFunc("POST /api/v1/sources", h.createSource)
	mux.HandleFunc("PATCH /api/v1/sources/{id}", h.patchSource)
	mux.HandleFunc("DELETE /api/v1/sources/{id}", h.deleteSource)
	mux.HandleFunc("GET /api/v1/settings", h.getSettings)
	mux.HandleFunc("PUT /api/v1/settings", h.putSettings)
	mux.HandleFunc("PATCH /api/v1/leads/{id}", h.patchLead)
	mux.HandleFunc("GET /api/v1/dlq", h.listDLQ)
	mux.HandleFunc("POST /api/v1/dlq/retry", h.retryDLQ)
	return mux
}

func (h *Handler) listSources(w http.ResponseWriter, r *http.Request) {
	sources, err := h.repo.ListSources(r.Context())
	if err != nil {
		h.log.Error("list sources failed", "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to list sources")
		return
	}
	if sources == nil {
		sources = []Source{}
	}
	api.WriteJSON(w, http.StatusOK, sources)
}

func (h *Handler) createSource(w http.ResponseWriter, r *http.Request) {
	var p CreateSourcePayload
	if err := api.DecodeJSON(w, r, &p); err != nil {
		return
	}
	if err := p.Validate(); err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	src, err := h.manager.Create(r.Context(), p)
	if err != nil {
		h.log.Error("create source failed", "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to create source")
		return
	}
	api.WriteJSON(w, http.StatusCreated, src)
}

func (h *Handler) patchSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p UpdateSourcePayload
	if err := api.DecodeJSON(w, r, &p); err != nil {
		return
	}
	if p.Enabled == nil {
		api.WriteError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	src, err := h.manager.SetEnabled(r.Context(), id, *p.Enabled)
	if errors.Is(err, ErrNotFound) {
		api.WriteError(w, http.StatusNotFound, "source not found")
		return
	}
	if err != nil {
		h.log.Error("update source failed", "id", id, "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to update source")
		return
	}
	api.WriteJSON(w, http.StatusOK, src)
}

func (h *Handler) deleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.manager.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "source not found")
			return
		}
		h.log.Error("delete source failed", "id", id, "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to delete source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) patchLead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p UpdateLeadPayload
	if err := api.DecodeJSON(w, r, &p); err != nil {
		return
	}
	if err := p.Validate(); err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.leads.UpdateLead(r.Context(), id, p)
	if errors.Is(err, ErrInvalidLeadID) {
		api.WriteError(w, http.StatusBadRequest, "invalid lead id")
		return
	}
	if errors.Is(err, ErrLeadNotFound) {
		api.WriteError(w, http.StatusNotFound, "lead not found")
		return
	}
	if err != nil {
		h.log.Error("update lead failed", "id", id, "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to update lead")
		return
	}
	api.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, h.settings.Get())
}

func (h *Handler) listDLQ(w http.ResponseWriter, r *http.Request) {
	if h.dlq == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "dlq store unavailable")
		return
	}
	ctx := r.Context()
	count, err := h.dlq.Count(ctx)
	if err != nil {
		h.log.Error("dlq count failed", "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to count dead letters")
		return
	}
	items, err := h.dlq.List(ctx, dlqListLimitParam(r))
	if err != nil {
		h.log.Error("dlq list failed", "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to list dead letters")
		return
	}
	if items == nil {
		items = []dlq.DLQItem{}
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"count": count, "items": items})
}

// dlqListLimitParam reads the optional ?limit=N query parameter. Missing,
// invalid, or non-positive values fall back to 50; values above 100 are
// clamped to 100.
func dlqListLimitParam(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}

func (h *Handler) retryDLQ(w http.ResponseWriter, r *http.Request) {
	if h.dlq == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "dlq store unavailable")
		return
	}
	n, err := h.dlq.RetryAll(r.Context())
	if err != nil {
		h.log.Error("dlq retry-all failed", "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to queue dead letter retries")
		return
	}
	h.log.Info("dlq retry-all queued", "rows", n)
	api.WriteJSON(w, http.StatusOK, map[string]any{"queued": n})
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var s EngineSettings
	if err := api.DecodeJSON(w, r, &s); err != nil {
		return
	}
	if err := s.Validate(); err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := h.settings.Put(r.Context(), s)
	if err != nil {
		h.log.Error("put settings failed", "err", err)
		api.WriteError(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}
