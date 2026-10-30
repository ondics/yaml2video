package handlers

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ondics/yaml2video/internal/render"
	"github.com/ondics/yaml2video/internal/server/api"
	"github.com/ondics/yaml2video/internal/server/controller"
)

type RenderRequest struct {
	ID       string         `json:"id"`
	Video    map[string]any `json:"video"`
	Template map[string]any `json:"template"`
}

func (h *Handler) Render(w http.ResponseWriter, r *http.Request) {
	var body RenderRequest
	if err := api.BindJSON(w, r, &body); err != nil {
		status := http.StatusBadRequest
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			status = http.StatusRequestEntityTooLarge
		}

		api.JSON(w, status, api.Map{"error": "invalid render request: " + err.Error()})
		return
	}

	if body.Video == nil {
		api.JSON(w, http.StatusBadRequest, api.Map{"error": "video must be a JSON object"})
		return
	}
	if body.Template == nil {
		api.JSON(w, http.StatusBadRequest, api.Map{"error": "template must be a JSON object"})
		return
	}

	id := body.ID
	idValid := id == "" ||
		len(id) > 512 ||
		!utf8.ValidString(id) ||
		strings.ContainsAny(id, "\x00\r\n")
	if idValid {
		api.JSON(w, http.StatusBadRequest, api.Map{
			"error": "provide an id (max 512 bytes) and video and template JSON objects",
		})
		return
	}

	video, err := json.Marshal(body.Video)
	if err != nil {
		api.JSON(w, http.StatusInternalServerError, api.Map{"error": "cannot encode video document"})
		return
	}
	template, err := json.Marshal(body.Template)
	if err != nil {
		api.JSON(w, http.StatusInternalServerError, api.Map{"error": "cannot encode template document"})
		return
	}

	h.serveRender(w, r, id, func() (*controller.RenderJob, error) {
		return h.Controller.PrepareRender(id, video, template)
	})
}

func (h *Handler) RenderLocal(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		api.JSON(w, http.StatusBadRequest, api.Map{"error": "invalid query parameters"})
		return
	}
	path := query.Get("path")
	if strings.TrimSpace(path) == "" {
		api.JSON(w, http.StatusBadRequest, api.Map{"error": "path to a video YAML file is required"})
		return
	}

	id := rand.Text()
	h.serveRender(w, r, id, func() (*controller.RenderJob, error) {
		return h.Controller.PrepareLocalRender(id, path, query.Get("template"), query.Get("dir"))
	})
}

func (h *Handler) serveRender(w http.ResponseWriter, r *http.Request, id string, prepare func() (*controller.RenderJob, error)) {
	api.ServeSSE(w, r, func(stream *api.SSE) {
		job, err := prepare()
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, controller.ErrIdentityExists) {
				status = http.StatusConflict
			} else if _, ok := errors.AsType[controller.InvalidPlanError](err); ok {
				status = http.StatusBadRequest
			}

			api.JSON(w, status, api.Map{"error": err.Error()})
			return
		}
		defer job.Close()

		if err := stream.Send("progress", api.Map{
			"id":      id,
			"stage":   "starting",
			"percent": 0,
		}); err != nil {
			return
		}

		if err := job.Run(stream.Context(), func(progress render.Progress) {
			// Send cancels the stream context on failure, which stops the renderer.
			stream.Send("progress", struct {
				ID string `json:"id"`
				render.Progress
			}{id, progress})
		}); err != nil {
			stream.Send("error", api.Map{
				"id":    id,
				"error": err.Error(),
			})
			return
		}

		stream.Send("complete", api.Map{
			"id":       id,
			"url":      renderURL(id),
			"download": downloadURL(id),
		})
	})
}
