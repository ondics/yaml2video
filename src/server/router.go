package server

import (
	"net/http"

	"github.com/ondics/yaml2video/src/server/internal/controller"
	"github.com/ondics/yaml2video/src/server/internal/handlers"
)

func NewRouter(backend *controller.Controller) http.Handler {
	h := &handlers.Handler{Controller: backend}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /fonts", h.ListFonts)
	mux.HandleFunc("GET /render/local", h.RenderLocal)
	mux.HandleFunc("POST /render", h.Render)
	mux.HandleFunc("GET /renders/{id}", h.GetRender)
	mux.HandleFunc("GET /renders/{id}/download", h.DownloadRender)
	mux.HandleFunc("GET /renders", h.ListRenders)
	return Chain(mux, Recover)
}
