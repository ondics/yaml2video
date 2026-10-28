package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ondics/yaml2video/src/render"
	"golang.org/x/image/font/gofont/goregular"
)

type fontsTransport func(*http.Request) (*http.Response, error)

func (f fontsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGetFonts(t *testing.T) {
	bundled := []render.FontFamily{
		{Family: "Go", Sources: []string{"bundled"}, AvailableOffline: true},
		{Family: "Go Mono", Sources: []string{"bundled"}, AvailableOffline: true},
	}
	for _, test := range []struct {
		name        string
		metadata    bool
		cached      bool
		online      bool
		wantFonts   []render.FontFamily
		wantWarning bool
	}{
		{name: "offline bundled", wantFonts: bundled},
		{
			name: "offline cached metadata and instance", metadata: true, cached: true,
			wantFonts: []render.FontFamily{
				{Family: "Cached Sans", Sources: []string{"google"}, Cached: true, AvailableOffline: true},
				bundled[0], bundled[1],
				{Family: "Metadata Only", Sources: []string{"google"}},
			},
		},
		{
			name: "offline cached instance without metadata", cached: true,
			wantFonts: []render.FontFamily{
				{Family: "Go", Sources: []string{"bundled", "google"}, Cached: true, AvailableOffline: true},
				bundled[1],
			},
		},
		{name: "metadata unavailable", online: true, wantFonts: bundled, wantWarning: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := t.TempDir()
			t.Setenv("YAML2VIDEO_CACHE_DIR", cache)
			t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
			t.Setenv("YAML2VIDEO_OFFLINE", "1")
			if test.online {
				t.Setenv("YAML2VIDEO_OFFLINE", "")
			}
			calls := 0
			previous := http.DefaultTransport
			http.DefaultTransport = fontsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !test.online || r.URL.String() != "https://fonts.google.com/metadata/fonts" {
					t.Errorf("unexpected network request: %s", r.URL)
				}
				return nil, errors.New("metadata unavailable")
			})
			t.Cleanup(func() { http.DefaultTransport = previous })

			dir := filepath.Join(cache, "fonts", "google")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if test.metadata {
				path := filepath.Join(dir, "families.json")
				if err := os.WriteFile(path, []byte(`{"familyMetadataList":[{"family":"Cached Sans"},{"family":"Metadata Only"}]}`), 0o600); err != nil {
					t.Fatal(err)
				}
				// Offline discovery must use even stale metadata without refreshing it.
				old := time.Unix(1, 0)
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if test.cached {
				// A local Go font stands in for a legacy cached Google instance;
				// metadata, when present, recovers its family from the cache filename.
				if err := os.WriteFile(filepath.Join(dir, "Cached-Sans-400.ttf"), goregular.TTF, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			response := httptest.NewRecorder()
			NewRouter(nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/fonts", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", response.Code, response.Body)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, want JSON", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			var catalog render.FontCatalog
			decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&catalog); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(catalog.Fonts, test.wantFonts) {
				t.Fatalf("fonts = %+v, want %+v", catalog.Fonts, test.wantFonts)
			}
			if test.wantWarning {
				if len(catalog.Warnings) != 1 || !strings.Contains(catalog.Warnings[0], "metadata unavailable") {
					t.Fatalf("warnings = %v, want metadata failure", catalog.Warnings)
				}
			} else if len(catalog.Warnings) != 0 || bytes.Contains(response.Body.Bytes(), []byte(`"warnings"`)) {
				t.Fatalf("warnings should be omitted: %s", response.Body)
			}
			if bytes.Contains(response.Body.Bytes(), []byte(cache)) || bytes.Contains(response.Body.Bytes(), []byte(".ttf")) {
				t.Fatalf("file path leaked: %s", response.Body)
			}

			shared, err := json.Marshal(render.ListFonts())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimSpace(response.Body.Bytes()), shared) {
				t.Fatalf("HTTP response differs from shared catalog: %s; shared: %s", response.Body, shared)
			}
			if !test.online && calls != 0 {
				t.Fatalf("offline discovery made %d network requests", calls)
			}
			if test.online && calls != 2 {
				t.Fatalf("metadata failure was not exercised by both calls: %d requests", calls)
			}
		})
	}
}

func TestFontsMethodNotAllowed(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(nil).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/fonts", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", response.Code)
	}
}
