package render

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ondics/yaml2video/src/project"
)

func TestCompileRejectsMissingFonts(t *testing.T) {
	for _, kind := range []string{"text", "subtitle"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("YAML2VIDEO_OFFLINE", "1")
			t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
			const family = "YAML2Video Nonexistent Regression Font"
			content := `    layers:
      - type: text
        text: [Hello]
`
			if kind == "subtitle" {
				content = `    subtitles:
      - duration: 1
        text: [Hello]
`
			}
			value, err := project.Parse([]byte(fmt.Sprintf(`video:
  width: 320
  height: 240
  fps: 30
  background: black
defaults:
  %s:
    font: %s
    size: 24
    color: white
scenes:
  - id: missing-font
    duration: 1
%s`, kind, family, content)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compile(value, Options{WorkDir: t.TempDir()})
			if err == nil {
				t.Fatal("expected compilation to reject an unavailable font")
			}
			for _, want := range []string{family, "Go Mono"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("missing-font error does not contain %q: %v", want, err)
				}
			}
			if !strings.Contains(strings.ReplaceAll(err.Error(), "Go Mono", ""), "Go") {
				t.Errorf("missing-font error does not suggest Go separately from Go Mono: %v", err)
			}
			if !errors.Is(err, errOffline) {
				t.Errorf("missing-font error does not wrap its cause: %v", err)
			}
		})
	}
}

// Without a font in the defaults, text and subtitles use Go, and bold spans
// bring Go Bold into the render's fonts.
func TestCompileDefaultsUnsetFontToGo(t *testing.T) {
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	value, err := project.Parse([]byte(`video:
  width: 320
  height: 240
  fps: 30
  background: black
defaults:
  text:
    size: 24
    color: white
  subtitle:
    size: 20
    color: white
scenes:
  - id: no-font
    duration: 1
    layers:
      - type: text
        text:
          - content: Hello
          - content: bold
            bold: true
    subtitles:
      - duration: 1
        text:
          - content: Hi
          - content: bold
            bold: true
`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Compile(value, Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	scene := plan.Scenes[0]
	if scene.Layers[0].Font != "Go" || scene.Subtitles[0].Font != "Go" {
		t.Errorf("fonts %q, %q", scene.Layers[0].Font, scene.Subtitles[0].Font)
	}
	fonts := plan.bundledFonts()
	if !slices.ContainsFunc(fonts, func(path string) bool { return filepath.Base(path) == "Go-Bold.ttf" }) {
		t.Errorf("bold face not bundled: %v", fonts)
	}
}

// Bold spans name the bold face, which can be an instance with a family name
// of its own.
func TestASSSpansUseBoldFace(t *testing.T) {
	text := assSpans([]TextSpan{{Content: "a"}, {Content: "b", Bold: true}}, "Fake y2v 400", "Fake y2v 700", 20, "0xFFFFFF", 1, nil, false)
	if !strings.Contains(text, `\fnFake y2v 400\`) || !strings.Contains(text, `\fnFake y2v 700\`) {
		t.Errorf("spans %s", text)
	}
}
