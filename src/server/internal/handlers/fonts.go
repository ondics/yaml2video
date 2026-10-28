package handlers

import (
	"net/http"

	"github.com/ondics/yaml2video/src/render"
	"github.com/ondics/yaml2video/src/server/internal/api"
)

func (h *Handler) ListFonts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	api.JSON(w, http.StatusOK, render.ListFonts())
}
