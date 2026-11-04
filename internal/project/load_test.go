package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ondics/yaml2video/internal/project"
	"github.com/ondics/yaml2video/internal/render"
)

func TestLoadComposesSlidesFromMultipleTemplates(t *testing.T) {
	directory := t.TempDir()
	projectPath := writeFixture(t, directory, "video.yaml", `
version: 1
video:
  width: 1920
  height: 1080
  fps: 30
  background: "#101010"
defaults:
  text:
    font: Go
    size: 48
    color: white
music:
  path: music.mp3
  volume: 1
slides:
  - type: intro
    image: intro.jpg
    text: Stadt Stuttgart
  - type: screenshot
    image: map.png
    textline1: Unfälle in Stuttgart?
    textline2: kein Problem mehr!
    textline3: mit der neuen Unfall-Atlas-App
    duration: 4s
`)
	introTemplate := writeFixture(t, directory, "intro.yaml", `
video:
  width: 640
  height: 360
  fps: 24
  background: black
defaults:
  text:
    font: Template Font
    size: 24
    color: yellow
music:
  path: template.mp3
  volume: 0.25
slide_templates:
  - name: intro
    required: image, text
    duration: 5s
    layers:
      - type: image
        path: "{{image}}"
        fit: cover
      - type: text
        text:
          - "{{ text }}"
        transform: bottom-center
`)
	screenshotTemplate := writeFixture(t, directory, "screenshot.yaml", `
slide_templates:
  - name: screenshot
    required: [image, textline1, textline2, textline3]
    duration: 5s
    layers:
      - type: image
        path: "{{ image }}"
        fit: contain
      - type: text
        text:
          - "{{textline1}}"
          - "\n{{textline2}}"
          - "\n{{textline3}}"
        transform: center
`)

	loaded, err := project.Load(projectPath, project.LoadOptions{
		TemplatePaths: []string{introTemplate, screenshotTemplate},
	})
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Video.Width != 1920 || loaded.Video.Height != 1080 || loaded.Video.FPS != 30 {
		t.Errorf("video = %#v", loaded.Video)
	}
	if loaded.Music.Path != "music.mp3" || loaded.Music.Volume != 1 {
		t.Errorf("music = %#v", loaded.Music)
	}
	if _, err := render.Compile(loaded, render.Options{}); err != nil {
		t.Fatalf("compile expanded project: %v", err)
	}

	if len(loaded.Scenes) != 2 {
		t.Fatalf("scene count = %d, want 2", len(loaded.Scenes))
	}

	intro := loaded.Scenes[0]
	if intro.ID != "intro-001" || *intro.Duration != 5 {
		t.Errorf("intro = %#v", intro)
	}
	if intro.Layers[0].Path != "intro.jpg" || string(*intro.Layers[1].Text[0].Short) != "Stadt Stuttgart" {
		t.Errorf("intro layers = %#v", intro.Layers)
	}

	screenshot := loaded.Scenes[1]
	if screenshot.ID != "screenshot-002" || *screenshot.Duration != 4 {
		t.Errorf("screenshot = %#v", screenshot)
	}
	if screenshot.Layers[0].Path != "map.png" {
		t.Errorf("screenshot image path = %q", screenshot.Layers[0].Path)
	}
}

func TestLoadUsesTemplateFallbackConfiguration(t *testing.T) {
	directory := t.TempDir()
	templatePath := writeFixture(t, directory, "slides.yaml", `
video:
  width: 320
  height: 240
  fps: 30
  background: black
defaults:
  text:
    font: Arial
    size: 24
    color: white
music:
  path: template.mp3
  volume: 0.5
slide_templates:
  - name: image
    required: image
    duration: 1s
    layers:
      - type: image
        path: "{{image}}"
`)
	projectPath := writeFixture(t, directory, "video.yaml", `
slides:
  - type: image
    image: still.png
`)

	loaded, err := project.Load(projectPath, project.LoadOptions{
		TemplatePaths: []string{templatePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Video.Width != 320 || loaded.Defaults.Text.Font != "Arial" {
		t.Errorf("template configuration was not applied: %#v", loaded)
	}
	if loaded.Music.Path != "template.mp3" || loaded.Music.Volume != 0.5 {
		t.Errorf("music = %#v", loaded.Music)
	}
}

func TestLoadReportsMissingRequiredSlideValue(t *testing.T) {
	directory := t.TempDir()
	templatePath := writeFixture(t, directory, "slides.yaml", `
slide_templates:
  - name: intro
    required: image, text
    duration: 5s
    layers:
      - type: image
        path: "{{image}}"
`)
	projectPath := writeFixture(t, directory, "video.yaml", `
video:
  width: 320
  height: 240
  fps: 30
  background: black
slides:
  - type: intro
    image: intro.jpg
`)

	_, err := project.Load(projectPath, project.LoadOptions{
		TemplatePaths: []string{templatePath},
	})
	if err == nil {
		t.Fatal("expected a required-value error")
	}

	const expected = `slide 1 (intro): required value "text" is missing`
	if err.Error() != expected {
		t.Errorf("error = %q, want %q", err, expected)
	}
}

func writeFixture(t *testing.T, directory, name, content string) string {
	t.Helper()

	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}
