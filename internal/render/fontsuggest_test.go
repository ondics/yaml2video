package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/fontscan"
	"golang.org/x/image/font/gofont/gomono"
)

func TestMissingFontSuggestions(t *testing.T) {
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "on")
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	resetGoogleFamilies(t)
	// Inject an index so this test doesn't depend on installed fonts.
	b := newFontBook(nil)
	b.sysOnce.Do(func() {
		b.system = []fontscan.Footprint{{Family: font.NormalizeFamily("Roboto")}}
	})
	catalog := `{"familyMetadataList":[{"family":"Roboto Slab"},{"family":"Open Sans"}]}`
	if err := os.WriteFile(filepath.Join(fontCacheDir("google"), "families.json"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := b.face("Robotto", 400, 0)
	if err == nil || f != nil {
		t.Fatalf("missing family must fail, got face %v, error %v", f, err)
	}
	message := err.Error()
	for _, want := range []string{`font "Robotto"`, `"roboto" (system)`, `"Roboto Slab" (Google Fonts)`, `"Go" (bundled)`, "not on Google Fonts"} {
		if !strings.Contains(message, want) {
			t.Errorf("error missing %q: %s", want, message)
		}
	}
	if strings.Index(message, `"roboto" (system)`) > strings.Index(message, `"Go" (bundled)`) {
		t.Errorf("similar family should rank ahead of unrelated fonts: %s", message)
	}
}

func TestMissingFontIsNotCachedAsFallback(t *testing.T) {
	dir := t.TempDir()
	b := newFontBook([]string{dir})
	if f, err := b.face("Go Monoo", 400, 0); err == nil || f != nil {
		t.Fatalf("unexpected substitution: %v, %v", f, err)
	}
	if len(b.faces) != 0 {
		t.Fatal("a missing family must not be cached as a successful resolution")
	}
	if err := os.WriteFile(filepath.Join(dir, "mono.ttf"), gomono.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	// A new book sees newly installed project fonts and offers them by name.
	b = newFontBook([]string{dir})
	_, err := b.face("Go Monoo", 400, 0)
	if err == nil || !strings.Contains(err.Error(), `"gomono" (project)`) {
		t.Fatalf("expected project font suggestion, got %v", err)
	}
}

func TestFontNameDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"roboto", "robotto", 1}, {"", "abc", 3}, {"go", "go", 0}, {"字形", "字体", 1},
	} {
		if got := fontNameDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("distance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
