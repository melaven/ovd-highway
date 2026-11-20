package controlplane

import (
	"errors"
	"log/slog"
	"net/http"

	"ovd/internal/api"
)

// Handler exposes the Control Plane REST contract under /api/v1/*.
// It is intended to be mounted behind api.BearerAuth and api.CORS.
type Handler struct {
	repo     SourceRepo
	settings *SettingsStore
	manager  *SourceManager
	log      *slog.Logger
}

func NewHandler(repo SourceRepo, settings *SettingsStore, manager *SourceManager, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		repo:     repo,
		settings: settings,
		manager:  manager,
		log:      log.With("component", "controlplane_api"),
	}
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

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, h.settings.Get())
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
