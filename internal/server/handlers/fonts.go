package handlers

import (
	"net/http"

	"github.com/ondics/yaml2video/internal/render"
	"github.com/ondics/yaml2video/internal/server/api"
)

func (h *Handler) ListFonts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	api.JSON(w, http.StatusOK, render.ListFonts())
}
