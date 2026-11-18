package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/ondics/yaml2video/internal/server/controller"
)

// Keep the example's required document fields, but use short, text-free
// scenes so the integration tests need neither downloaded fonts nor media.
func previewDocuments(t *testing.T) (video, template []byte) {
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
	documents["video"]["content"].(map[string]any)["slides"] = []any{map[string]any{"duration": "0.2s"}}
	tmpl := documents["template"]
	tmpl["format"] = map[string]any{"aspect_ratio": "16:9", "width": 96, "height": 54, "fps": 10}
	defaults := tmpl["defaults"].(map[string]any)
	defaults["typography"].(map[string]any)["font_family"] = "Go"
	defaults["timing"] = map[string]any{"intro_duration": "0.2s", "slide_duration": "0.2s", "outro_duration": "0.2s"}

	layouts := make(map[string]any)
	for role, color := range map[string]string{"intro": "#FF0000", "slide": "#000000", "outro": "#0000FF"} {
		layouts[role] = map[string]any{"background": color, "elements": []any{}}
	}
	tmpl["layout"] = layouts
	var err error
	video, err = json.Marshal(documents["video"])
	if err != nil {
		t.Fatal(err)
	}
	template, err = json.Marshal(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	return video, template
}

func previewRequest(t *testing.T, router http.Handler, method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := httptest.NewRequest(method, target, bytes.NewReader(body)).WithContext(ctx)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func previewJobDir(dataDir, id string) string {
	return filepath.Join(dataDir, "jobs", fmt.Sprintf("%x", sha256.Sum256([]byte(id))))
}

func previewMetadata(t *testing.T, dataDir, id string) controller.JobMetadata {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(previewJobDir(dataDir, id), "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata controller.JobMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func writePreviewMetadata(t *testing.T, dataDir string, metadata controller.JobMetadata) {
	t.Helper()
	dir := previewJobDir(dataDir, metadata.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type previewCompletion struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Download string `json:"download"`
	Preview  string `json:"preview"`
}

func previewCompleteEvent(t *testing.T, response *httptest.ResponseRecorder) previewCompletion {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("render: status = %d, Content-Type = %q; body: %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var complete previewCompletion
	count := 0
	for _, frame := range strings.Split(response.Body.String(), "\n\n") {
		if strings.HasPrefix(frame, "event: error\n") {
			t.Fatalf("render failed: %s", frame)
		}
		if strings.HasPrefix(frame, "event: complete\ndata: ") {
			count++
			if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: complete\ndata: ")), &complete); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 || complete.ID == "" {
		t.Fatalf("want one complete event with an ID, got %d: %s", count, response.Body.String())
	}
	base := "/renders/" + url.PathEscape(complete.ID)
	if complete.URL != base || complete.Download != base+"/download" || complete.Preview != base+"/preview" {
		t.Fatalf("unexpected complete URLs: %+v", complete)
	}
	return complete
}

func TestPreviewRenderIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	video, template := previewDocuments(t)

	for _, endpoint := range []string{"POST /render", "GET /render/local"} {
		t.Run(endpoint, func(t *testing.T) {
			dataDir := t.TempDir()
			backend, err := controller.New(controller.Config{DataDir: dataDir})
			if err != nil {
				t.Fatal(err)
			}
			router := NewRouter(backend)
			var response *httptest.ResponseRecorder
			if endpoint == "POST /render" {
				body, err := json.Marshal(map[string]any{
					"id": "preview/nested movie", "video": json.RawMessage(video), "template": json.RawMessage(template),
				})
				if err != nil {
					t.Fatal(err)
				}
				response = previewRequest(t, router, http.MethodPost, "/render", body, map[string]string{"Content-Type": "application/json"})
			} else {
				dir := t.TempDir()
				for name, data := range map[string][]byte{"video.yaml": video, "template.yaml": template} {
					if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				response = previewRequest(t, router, http.MethodGet, "/render/local?"+url.Values{"path": {filepath.Join(dir, "video.yaml")}}.Encode(), nil, nil)
			}
			complete := previewCompleteEvent(t, response)
			if endpoint == "POST /render" && complete.ID != "preview/nested movie" {
				t.Fatalf("complete ID = %q, want original slash-containing ID", complete.ID)
			}
			before := previewMetadata(t, dataDir, complete.ID)
			if !before.Ready || before.FreeableAt != nil {
				t.Fatalf("unexpected completed metadata: %+v", before)
			}
			jobDir := previewJobDir(dataDir, complete.ID)
			for _, name := range []string{"output.mp4", "preview.png"} {
				if info, err := os.Stat(filepath.Join(jobDir, name)); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
					t.Fatalf("missing/nonregular/empty adjacent artifact %s: %v, %v", name, info, err)
				}
			}

			get := previewRequest(t, router, http.MethodGet, complete.Preview, nil, nil)
			if get.Code != http.StatusOK || get.Header().Get("Content-Type") != "image/png" || get.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("preview GET: status = %d, headers = %v; body: %s", get.Code, get.Header(), get.Body.String())
			}
			img, err := png.Decode(bytes.NewReader(get.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if bounds := img.Bounds(); bounds.Dx() != 96 || bounds.Dy() != 54 {
				t.Fatalf("PNG dimensions = %v, want 96x54", bounds)
			}
			// The outro is blue; a red preview proves the first scene was used.
			r, g, b, a := img.At(48, 27).RGBA()
			if r < 240*257 || g > 15*257 || b > 15*257 || a != 65535 {
				t.Fatalf("preview pixel = (%d,%d,%d,%d), want opaque red first frame", r, g, b, a)
			}
			diskPNG, err := os.ReadFile(filepath.Join(jobDir, "preview.png"))
			if err != nil || !bytes.Equal(diskPNG, get.Body.Bytes()) {
				t.Fatalf("served PNG differs from adjacent preview.png: %v", err)
			}
			head := previewRequest(t, router, http.MethodHead, complete.Preview, nil, nil)
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "image/png" || head.Header().Get("Cache-Control") != "no-store" || head.Header().Get("Content-Length") != fmt.Sprint(len(diskPNG)) {
				t.Fatalf("preview HEAD: status = %d, headers = %v, body length = %d", head.Code, head.Header(), head.Body.Len())
			}
			partial := previewRequest(t, router, http.MethodGet, complete.Preview, nil, map[string]string{"Range": "bytes=0-7"})
			if partial.Code != http.StatusPartialContent || !bytes.Equal(partial.Body.Bytes(), diskPNG[:8]) || partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-7/%d", len(diskPNG)) || partial.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("preview range: status = %d, headers = %v, body = %q", partial.Code, partial.Header(), partial.Body.Bytes())
			}
			if after := previewMetadata(t, dataDir, complete.ID); !reflect.DeepEqual(after, before) {
				t.Fatalf("preview requests changed retention metadata: before = %+v, after = %+v", before, after)
			}

			for _, method := range []string{http.MethodHead, http.MethodGet} {
				headers := map[string]string{}
				wantStatus := http.StatusOK
				if method == http.MethodGet {
					headers["Range"] = "bytes=0-7"
					wantStatus = http.StatusPartialContent
				}
				result := previewRequest(t, router, method, complete.Download, nil, headers)
				if result.Code != wantStatus {
					t.Fatalf("video %s: status = %d, want %d", method, result.Code, wantStatus)
				}
				if after := previewMetadata(t, dataDir, complete.ID); !reflect.DeepEqual(after, before) {
					t.Fatalf("video HEAD/range changed retention: %+v", after)
				}
			}
			started := time.Now().UTC()
			download := previewRequest(t, router, http.MethodGet, complete.Download, nil, nil)
			mp4, err := os.ReadFile(filepath.Join(jobDir, "output.mp4"))
			if err != nil || download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), mp4) {
				t.Fatalf("full video download: status = %d, disk error = %v", download.Code, err)
			}
			after := previewMetadata(t, dataDir, complete.ID)
			if after.FreeableAt == nil || after.FreeableAt.Before(started) || after.FreeableAt.After(time.Now().UTC()) || !after.ExpiresAt().Before(before.ExpiresAt()) {
				t.Fatalf("full video download did not shorten retention: before = %+v, after = %+v", before, after)
			}
			unchanged := after
			unchanged.FreeableAt = nil
			if !reflect.DeepEqual(unchanged, before) {
				t.Fatalf("download changed metadata beyond freeable_at: %+v", after)
			}
			if result := previewRequest(t, router, http.MethodGet, complete.Preview, nil, nil); result.Code != http.StatusOK || !bytes.Equal(result.Body.Bytes(), diskPNG) {
				t.Fatalf("preview unavailable after download: status = %d", result.Code)
			}
			if got := previewMetadata(t, dataDir, complete.ID); !reflect.DeepEqual(got, after) {
				t.Fatalf("preview reset shortened retention: before = %+v, after = %+v", after, got)
			}

			// Age the shared download timestamp instead of sleeping for retention.
			expiredDownload := time.Now().UTC().Add(-time.Hour)
			after.FreeableAt = &expiredDownload
			writePreviewMetadata(t, dataDir, after)
			for _, target := range []string{complete.Preview, complete.Download} {
				if result := previewRequest(t, router, http.MethodGet, target, nil, nil); result.Code != http.StatusNotFound {
					t.Errorf("shared download expiry: GET %s = %d, want 404", target, result.Code)
				}
			}
		})
	}
}

func TestPreviewUnavailableIntegration(t *testing.T) {
	dataDir := t.TempDir()
	backend, err := controller.New(controller.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(backend)
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	downloaded := now.Add(-time.Hour)
	for _, tc := range []struct {
		name     string
		metadata controller.JobMetadata
		missing  bool
	}{
		{name: "unknown"},
		{name: "missing PNG", metadata: controller.JobMetadata{ID: "missing", CreatedAt: now, Ready: true}, missing: true},
		{name: "incomplete", metadata: controller.JobMetadata{ID: "incomplete", CreatedAt: now}},
		{name: "expired creation", metadata: controller.JobMetadata{ID: "expired", CreatedAt: now.Add(-8 * 24 * time.Hour), Ready: true}},
		{name: "expired download", metadata: controller.JobMetadata{ID: "downloaded", CreatedAt: now, FreeableAt: &downloaded, Ready: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.metadata.ID
			if id == "" {
				id = "unknown"
			} else {
				writePreviewMetadata(t, dataDir, tc.metadata)
				dir := previewJobDir(dataDir, id)
				if err := os.WriteFile(filepath.Join(dir, "output.mp4"), []byte("present output"), 0o600); err != nil {
					t.Fatal(err)
				}
				if !tc.missing {
					if err := os.WriteFile(filepath.Join(dir, "preview.png"), pngData.Bytes(), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				result := previewRequest(t, router, method, "/renders/"+url.PathEscape(id)+"/preview", nil, nil)
				if result.Code != http.StatusNotFound {
					t.Errorf("%s unavailable preview: status = %d, want 404; body: %s", method, result.Code, result.Body.String())
				}
			}
		})
	}
}
