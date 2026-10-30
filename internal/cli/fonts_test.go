package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ondics/yaml2video/internal/render"
	"golang.org/x/image/font/gofont/goregular"
)

func TestParseFontsCommand(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, tc := range []struct {
		name        string
		args        []string
		help        bool
		invalid     bool
		offlineOnly bool
	}{
		{name: "fonts", args: []string{"fonts"}},
		{name: "offline only", args: []string{"fonts", "--offline-only"}, offlineOnly: true},
		{name: "offline false", args: []string{"fonts", "--offline-only=false"}},
		{name: "invalid boolean", args: []string{"fonts", "--offline-only=maybe"}, invalid: true},
		{name: "filter with positional argument", args: []string{"fonts", "--offline-only", "video.yaml"}, invalid: true},
		{name: "filter on serve", args: []string{"serve", "--offline-only"}, invalid: true},
		{name: "filter on render", args: []string{"video.yaml", "--offline-only"}, invalid: true},
		{name: "help", args: []string{"fonts", "--help"}, help: true},
		{name: "extra argument", args: []string{"fonts", "video.yaml"}, invalid: true},
		{name: "render flag", args: []string{"fonts", "-n"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = append([]string{"yaml2video"}, tc.args...)
			options, err := parseArgs()
			switch {
			case tc.help:
				if !errors.Is(err, flag.ErrHelp) {
					t.Fatalf("expected help, got %v", err)
				}
			case tc.invalid:
				if err == nil {
					t.Fatal("expected invalid command arguments")
				}
			default:
				if err != nil || !options.fonts || options.serve || options.videoPath != "" || options.offlineOnly != tc.offlineOnly {
					t.Fatalf("unexpected options %#v, error %v", options, err)
				}
			}
		})
	}
}

func TestFontCatalogWarningsGoToStderr(t *testing.T) {
	var output, warnings bytes.Buffer
	catalog := render.FontCatalog{
		Fonts:    []render.FontFamily{{Family: "Go", Sources: []string{"bundled", "system"}, AvailableOffline: true}},
		Warnings: []string{"Google Fonts metadata unavailable"},
	}
	if err := printFontCatalog(&output, &warnings, catalog); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "[Warning]") || warnings.String() != "[Warning] Google Fonts metadata unavailable\n" {
		t.Fatalf("warnings should only go to stderr: stdout=%q stderr=%q", &output, &warnings)
	}
	if !strings.Contains(output.String(), "bundled, system") {
		t.Fatalf("expected combined sources in table: %s", &output)
	}
}

func TestCLIFontsUsesSharedCatalogOffline(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("YAML2VIDEO_CACHE_DIR", cache)
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	google := filepath.Join(cache, "fonts", "google")
	if err := os.MkdirAll(google, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"families.json":       []byte(`{"familyMetadataList":[{"family":"Cached Sans"},{"family":"Metadata Only"}]}`),
		"Cached-Sans-400.ttf": goregular.TTF,
	} {
		if err := os.WriteFile(filepath.Join(google, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "yaml2video")
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/yaml2video").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	output, err := exec.CommandContext(ctx, binary, "fonts").Output()
	if err != nil {
		t.Fatalf("fonts: %v", err)
	}
	var expected, warnings bytes.Buffer
	if err := printFontCatalog(&expected, &warnings, render.ListFonts()); err != nil {
		t.Fatal(err)
	}
	if string(output) != expected.String() {
		t.Fatalf("CLI table differs from shared catalogue:\ngot %s\nwant %s", output, &expected)
	}
	filtered, err := exec.CommandContext(ctx, binary, "fonts", "--offline-only").Output()
	if err != nil {
		t.Fatalf("fonts --offline-only: %v", err)
	}
	if strings.Contains(string(filtered), "Metadata Only") || !strings.Contains(string(filtered), "3 font families.") {
		t.Fatalf("offline filter should exclude metadata-only families and update the count:\n%s", filtered)
	}
	for _, family := range []string{"Cached Sans", "Go", "Go Mono"} {
		if !strings.Contains(string(filtered), family) {
			t.Errorf("offline table missing %q:\n%s", family, filtered)
		}
	}
	for _, row := range []string{"FONT FAMILY SOURCE CACHED OFFLINE", "Cached Sans google yes yes", "Go bundled no yes", "Go Mono bundled no yes", "Metadata Only google no no"} {
		found := false
		for _, line := range strings.Split(string(output), "\n") {
			if strings.Join(strings.Fields(line), " ") == row {
				found = true
			}
		}
		if !found {
			t.Errorf("missing table row %q in:\n%s", row, output)
		}
	}
}
