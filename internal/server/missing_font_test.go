package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/ondics/yaml2video/internal/server/controller"
)

const missingFontFamily = "YAML2Video Nonexistent HTTP Regression Font"

func missingFontDocuments(t *testing.T) (video, template []byte) {
	t.Helper()
	documents := make(map[string]map[string]any)
	for _, name := range []string{"video", "template"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "simple", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		documents[name] = document
	}
	delete(documents["video"], "assets")
	tmpl := documents["template"]
	tmpl["defaults"].(map[string]any)["typography"].(map[string]any)["font_family"] = missingFontFamily
	// Keep the text-only intro and outro, avoiding external media and FFmpeg.
	sections := tmpl["composition"].(map[string]any)["sections"].([]any)
	tmpl["composition"].(map[string]any)["sections"] = []any{sections[0], sections[2]}
	video, err := json.Marshal(documents["video"])
	if err != nil {
		t.Fatal(err)
	}
	template, err = json.Marshal(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	return video, template
}

func TestMissingFontReturnsBadRequestAndServerRemainsUsable(t *testing.T) {
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	video, template := missingFontDocuments(t)
	directory := t.TempDir()
	videoPath := filepath.Join(directory, "video.yaml")
	for name, data := range map[string][]byte{"video.yaml": video, "template.yaml": template} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, endpoint := range []string{"POST /render", "GET /render/local"} {
		t.Run(endpoint, func(t *testing.T) {
			backend, err := controller.New(controller.Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(NewRouter(backend))
			defer server.Close()
			client := server.Client()
			client.Timeout = 10 * time.Second

			requestMissingFont := func() {
				t.Helper()
				var response *http.Response
				var err error
				if endpoint == "POST /render" {
					body, marshalErr := json.Marshal(map[string]any{
						"id": "missing-font", "video": json.RawMessage(video), "template": json.RawMessage(template),
					})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					response, err = client.Post(server.URL+"/render", "application/json", bytes.NewReader(body))
				} else {
					response, err = client.Get(server.URL + "/render/local?" + url.Values{"path": {videoPath}}.Encode())
				}
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body: %s", response.StatusCode, body)
				}
				if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
					t.Fatalf("Content-Type = %q, want JSON; body: %s", contentType, body)
				}
				var result struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(body, &result); err != nil {
					t.Fatalf("invalid JSON error response: %v; body: %s", err, body)
				}
				for _, want := range []string{missingFontFamily, "Go Mono"} {
					if !strings.Contains(result.Error, want) {
						t.Errorf("error missing %q: %s", want, result.Error)
					}
				}
				if !strings.Contains(strings.ReplaceAll(result.Error, "Go Mono", ""), "Go") {
					t.Errorf("error does not suggest Go separately from Go Mono: %s", result.Error)
				}
			}

			requestMissingFont()
			// A normal request must still succeed, with no failed job left behind.
			response, err := client.Get(server.URL + "/renders")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var renders []json.RawMessage
			if err := json.NewDecoder(response.Body).Decode(&renders); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || len(renders) != 0 {
				t.Fatalf("follow-up list: status = %d, renders = %s", response.StatusCode, renders)
			}
			// The same POST identity must be released, not rejected with 409.
			requestMissingFont()
		})
	}
}
