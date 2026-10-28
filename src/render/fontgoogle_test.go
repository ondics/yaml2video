package render

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
)

func TestGoogleFontOpticalSizeFallback(t *testing.T) {
	for _, tc := range []struct {
		name          string
		opticalStatus int
		weightCSS     bool
		fontStatus    int
		wantError     string
		wantQueries   int
	}{
		{name: "empty optical CSS", opticalStatus: 200, weightCSS: true, fontStatus: 200, wantQueries: 2},
		{name: "both CSS responses empty", opticalStatus: 200, fontStatus: 200, wantError: "default optical size also unavailable", wantQueries: 2},
		{name: "optical HTTP failure is not hidden", opticalStatus: 503, weightCSS: true, fontStatus: 200, wantError: "HTTP 503", wantQueries: 1},
		{name: "font download failure", opticalStatus: 200, weightCSS: true, fontStatus: 404, wantError: "HTTP 404", wantQueries: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YAML2VIDEO_OFFLINE", "")
			t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
			resetGoogleFamilies(t)
			metadata := `{"familyMetadataList":[{"family":"Inter","axes":[{"tag":"opsz","min":14,"max":32,"defaultValue":14},{"tag":"wght","min":100,"max":900,"defaultValue":400}]}]}`
			if err := os.WriteFile(filepath.Join(fontCacheDir("google"), "families.json"), []byte(metadata), 0o644); err != nil {
				t.Fatal(err)
			}
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/css2":
					query := r.URL.Query().Get("family")
					queries = append(queries, query)
					if strings.Contains(query, "opsz") {
						w.WriteHeader(tc.opticalStatus)
						return // Google's real response: HTTP 200, empty CSS.
					}
					if tc.weightCSS {
						fmt.Fprintf(w, "@font-face { src: url(http://%s/font.ttf) format('truetype'); }", r.Host)
					}
				case "/font.ttf":
					w.WriteHeader(tc.fontStatus)
					if tc.fontStatus == http.StatusOK {
						w.Write(gobold.TTF)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			oldCSS := googleCSS
			googleCSS = server.URL + "/css2"
			t.Cleanup(func() { googleCSS = oldCSS })

			face, err := newFontBook(nil).face("Inter", 700, 88)
			want := []string{"Inter:opsz,wght@32,700", "Inter:wght@700"}[:tc.wantQueries]
			if !reflect.DeepEqual(queries, want) {
				t.Fatalf("queries = %q, want %q", queries, want)
			}
			if tc.wantError != "" {
				if err == nil || face != nil || !strings.Contains(err.Error(), tc.wantError) || !strings.Contains(err.Error(), "is listed on Google Fonts but could not be loaded") {
					t.Fatalf("expected actionable download error %q, got %v, %v", tc.wantError, face, err)
				}
				if tc.opticalStatus == 200 && !tc.weightCSS && !errors.Is(err, errGoogleNoFontFile) {
					t.Fatalf("missing CSS error not preserved: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if face.Name != "Inter y2v 700" || filepath.Base(face.Path) != "Inter-700.ttf" || face.Weight != 600 {
				// Go Bold's weight class is 600; the fixture otherwise exercises the
				// actual download, rename, cache, and face-loading path.
				t.Fatalf("fallback must retain family and weight label, not claim an optical size: %+v", face)
			}
			if _, err := os.Stat(filepath.Join(fontCacheDir("google"), "Inter-700-o32.ttf")); !os.IsNotExist(err) {
				t.Fatalf("default optical font must not be cached as an exact optical instance: %v", err)
			}
			// The fallback cache remains usable offline in a fresh process.
			t.Setenv("YAML2VIDEO_OFFLINE", "1")
			resetGoogleFamilies(t)
			server.Close()
			offline, err := newFontBook(nil).face("Inter", 700, 88)
			if err != nil || offline.Path != face.Path {
				t.Fatalf("offline fallback = %v, %v", offline, err)
			}
			if _, err := newFontBook(nil).face("Inter", 400, 88); !errors.Is(err, errOffline) {
				t.Fatalf("uncached weights must still fail offline: %v", err)
			}
		})
	}
}
