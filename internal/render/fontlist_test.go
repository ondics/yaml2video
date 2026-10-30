package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/fontscan"
	"golang.org/x/image/font/gofont/goregular"
)

type fontListTransport func(*http.Request) (*http.Response, error)

func (f fontListTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func setupFontList(t *testing.T) {
	t.Helper()
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	old := googleClient
	googleClient = &http.Client{Transport: fontListTransport(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected network request: %s", r.URL)
		return nil, errors.New("network forbidden")
	})}
	t.Cleanup(func() { googleClient = old })
}

func writeFontListMetadata(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(fontCacheDir("google"), "families.json")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFontListFont(t *testing.T, dir, file, name string) string {
	t.Helper()
	data, err := renameFont(goregular.TTF, name)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListFontsOfflineBundledContract(t *testing.T) {
	setupFontList(t)
	got := ListFonts()
	want := FontCatalog{Fonts: []FontFamily{
		{Family: "Go", Sources: []string{"bundled"}, AvailableOffline: true},
		{Family: "Go Mono", Sources: []string{"bundled"}, AvailableOffline: true},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"fonts":[{"family":"Go","sources":["bundled"],"cached":false,"available_offline":true},{"family":"Go Mono","sources":["bundled"],"cached":false,"available_offline":true}]}` {
		t.Fatalf("unexpected JSON contract: %s", data)
	}
	listFonts(func() ([]string, error) {
		t.Fatal("system discovery must be disabled")
		return nil, nil
	})
}

func TestListFontsOfflineMetadataDedupSorting(t *testing.T) {
	setupFontList(t)
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "on")
	path := writeFontListMetadata(t, `{"familyMetadataList":[{"family":"zebra Sans"},{"family":"Go Mono"},{"family":"ALPHA Sans"},{"family":"go"},{"family":"AlphaSans"}]}`)
	old := time.Now().Add(-2 * googleListTTL)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	got := listFonts(func() ([]string, error) {
		return []string{"Alpha Sans", "Go Mono", "alpha sans"}, nil
	})
	want := FontCatalog{Fonts: []FontFamily{
		{Family: "Alpha Sans", Sources: []string{"system", "google"}, AvailableOffline: true},
		{Family: "Go", Sources: []string{"bundled", "google"}, AvailableOffline: true},
		{Family: "Go Mono", Sources: []string{"bundled", "system", "google"}, AvailableOffline: true},
		{Family: "zebra Sans", Sources: []string{"google"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestListFontsCachedInstances(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		t.Run(fmt.Sprintf("metadata=%v", metadata), func(t *testing.T) {
			setupFontList(t)
			if metadata {
				writeFontListMetadata(t, `{"familyMetadataList":[{"family":"Roboto Slab"},{"family":"Remote Only"},{"family":"Go"}]}`)
			}
			dir := fontCacheDir("google")
			writeFontListFont(t, dir, "Roboto-Slab-400-o24.ttf", "Roboto Slab y2v 400 o24")
			writeFontListFont(t, dir, "Roboto-Slab-700.ttf", "Roboto Slab y2v 700")
			writeFontListFont(t, dir, "Real-Hyphen-Sans-400.ttf", "Real-Hyphen Sans y2v 400")
			writeFontListFont(t, dir, "Go-400.ttf", "Go y2v 400")
			got := ListFonts()
			if len(got.Warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", got.Warnings)
			}
			found := map[string]FontFamily{}
			for _, f := range got.Fonts {
				found[f.Family] = f
				if strings.Contains(f.Family, "y2v") {
					t.Fatalf("internal name leaked: %+v", f)
				}
			}
			for _, name := range []string{"Roboto Slab", "Real-Hyphen Sans", "Go"} {
				f := found[name]
				if !f.Cached || !f.AvailableOffline {
					t.Errorf("cached family %q: %+v", name, f)
				}
			}
			if !reflect.DeepEqual(found["Go"].Sources, []string{"bundled", "google"}) {
				t.Errorf("sources not merged: %+v", found["Go"])
			}
			if metadata && (found["Remote Only"].AvailableOffline || found["Remote Only"].Cached) {
				t.Errorf("metadata is not an offline instance: %+v", found["Remote Only"])
			}
			wantLen := 4
			if metadata {
				wantLen++
			}
			if len(got.Fonts) != wantLen {
				t.Fatalf("instances not deduplicated: %+v", got)
			}
		})
	}
}

func TestListFontsCachedFilenameMetadata(t *testing.T) {
	setupFontList(t)
	writeFontListMetadata(t, `{"familyMetadataList":[{"family":"Original Family"}]}`)
	writeFontListFont(t, fontCacheDir("google"), "Original-Family-400.ttf", "Legacy Name")
	got := ListFonts()
	if len(got.Fonts) != 3 || got.Fonts[2].Family != "Original Family" || !got.Fonts[2].Cached {
		t.Fatalf("filename should recover metadata family: %+v", got)
	}
}

func TestListFontsCachedWithoutMetadataFallbacks(t *testing.T) {
	setupFontList(t)
	dir := fontCacheDir("google")
	writeFontListFont(t, dir, "Unrenamed-400.ttf", "Original Display Family")
	writeFontListFont(t, dir, "Filename-Only-400-o24.ttf", "")
	// Local variable-font instances are not downloaded Google files.
	writeFontListFont(t, fontCacheDir("instances"), "local.ttf", "Local Only y2v 400")
	got := ListFonts()
	want := FontCatalog{Fonts: []FontFamily{
		{Family: "Filename Only", Sources: []string{"google"}, Cached: true, AvailableOffline: true},
		{Family: "Go", Sources: []string{"bundled"}, AvailableOffline: true},
		{Family: "Go Mono", Sources: []string{"bundled"}, AvailableOffline: true},
		{Family: "Original Display Family", Sources: []string{"google"}, Cached: true, AvailableOffline: true},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestListFontsOnlineMetadataOnly(t *testing.T) {
	setupFontList(t)
	t.Setenv("YAML2VIDEO_OFFLINE", "")
	calls := 0
	googleClient.Transport = fontListTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != googleList {
			t.Fatalf("font downloads forbidden: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`)]}'
{"familyMetadataList":[{"family":"Online Sans"}]}`)), Header: make(http.Header)}, nil
	})
	got := ListFonts()
	if calls != 1 || len(got.Warnings) != 0 || len(got.Fonts) != 3 || got.Fonts[2].Family != "Online Sans" || got.Fonts[2].Cached || got.Fonts[2].AvailableOffline {
		t.Fatalf("calls=%d catalog=%+v", calls, got)
	}
	ListFonts()
	if calls != 1 {
		t.Fatalf("metadata disk cache not reused: %d calls", calls)
	}
}

func TestListFontsPartialFailures(t *testing.T) {
	setupFontList(t)
	t.Setenv("YAML2VIDEO_OFFLINE", "")
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "on")
	googleClient.Transport = fontListTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("metadata unavailable")
	})
	dir := fontCacheDir("google")
	writeFontListFont(t, dir, "Cached-Sans-400.ttf", "Cached Sans y2v 400")
	if err := os.WriteFile(filepath.Join(dir, "Broken-400.ttf"), []byte("not a font"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := listFonts(func() ([]string, error) { return []string{"Local Sans"}, errors.New("scan incomplete") })
	if len(got.Fonts) != 4 || len(got.Warnings) != 3 {
		t.Fatalf("partial catalog: %+v", got)
	}
	warnings := strings.Join(got.Warnings, "\n")
	for _, want := range []string{"scan incomplete", "metadata unavailable", "Broken-400.ttf"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing warning %q: %s", want, warnings)
		}
	}
}

func TestListFontsCacheIsolation(t *testing.T) {
	setupFontList(t)
	googleMu.Lock()
	old := googleFamilies
	googleFamilies = map[string]googleFamily{"polluted": {Family: "Polluted Global"}}
	googleMu.Unlock()
	t.Cleanup(func() {
		googleMu.Lock()
		googleFamilies = old
		googleMu.Unlock()
	})
	writeFontListMetadata(t, `{"familyMetadataList":[{"family":"First Cache"}]}`)
	if got := ListFonts(); len(got.Fonts) != 3 || got.Fonts[0].Family != "First Cache" {
		t.Fatalf("global metadata leaked: %+v", got)
	}
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	if got := ListFonts(); len(got.Fonts) != 2 || len(got.Warnings) != 0 {
		t.Fatalf("previous cache leaked: %+v", got)
	}
}

func TestListFontsInstalledDisplayNames(t *testing.T) {
	setupFontList(t)
	path := writeFontListFont(t, t.TempDir(), "system.ttf", "Installed Family With Spaces")
	prints := []fontscan.Footprint{
		{Family: font.NormalizeFamily("Installed Family With Spaces"), Location: font.FontID{File: path}},
		{Family: "duplicate", Location: font.FontID{File: path}},
		{Location: font.FontID{File: filepath.Join(t.TempDir(), "missing.ttf")}},
	}
	names, err := installedFontNames(prints, errors.New("partial scan"))
	if !reflect.DeepEqual(names, []string{"Installed Family With Spaces"}) || err == nil || !strings.Contains(err.Error(), "partial scan") || !strings.Contains(err.Error(), "missing.ttf") {
		t.Fatalf("names=%v error=%v", names, err)
	}
}
