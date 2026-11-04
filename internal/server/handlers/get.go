package handlers

import (
	"net/http"

	"github.com/ondics/yaml2video/internal/server/api"
)

func (h *Handler) ListRenders(w http.ResponseWriter, r *http.Request) {
	renders, err := h.Controller.ListRenders()
	if err != nil {
		api.JSON(w, http.StatusInternalServerError, api.Map{"error": "cannot list renders"})
		return
	}

	items := make([]api.Map, 0, len(renders))
	for _, render := range renders {
		items = append(items, api.Map{
			"id":         render.ID,
			"created_at": render.CreatedAt,
			"expires_at": render.ExpiresAt(),
			"download":   downloadURL(render.ID),
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	api.JSON(w, http.StatusOK, items)
}

func (h *Handler) GetRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	render, err := h.Controller.GetRender(id)
	if err != nil {
		api.JSON(w, http.StatusInternalServerError, api.Map{"error": "cannot list renders"})
		return
	}
	if render == nil {
		api.JSON(w, http.StatusNotFound, api.Map{
			"error": "Render not found",
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	api.JSON(w, http.StatusOK, api.Map{
		"id":         render.ID,
		"created_at": render.CreatedAt,
		"expires_at": render.ExpiresAt(),
		"download":   downloadURL(render.ID),
	})
}
