package render

import (
	"strings"
	"testing"

	"github.com/ondics/yaml2video/internal/project"
)

func TestRejectsCanvasRelativeCustomScaling(t *testing.T) {
	value, err := project.Parse([]byte(`
video:
  width: 320
  height: 240
  fps: 30
  background: black
scenes:
  - id: invalid-scale
    duration: 1
    layers:
      - type: image
        path: image.png
        transform:
          x: 0.5
          y: 0.5
          scale_x: 0.5
`))
	if err != nil {
		t.Fatal(err)
	}

	_, err = Compile(value, Options{})
	if err == nil || !strings.Contains(err.Error(), "scale_x requires an explicit layer width") {
		t.Fatalf("expected explicit-width error, got %v", err)
	}
}

func TestCompileLayerPixelDimensionsScaleWrapAndVideoTrim(t *testing.T) {
	value, err := project.Parse([]byte(`
video:
  width: 320
  height: 240
  fps: 30
  background: black
defaults:
  text:
    font: Go
    size: 20
    color: white
scenes:
  - id: layout
    duration: 2
    layers:
      - type: image
        path: image.png
        width: 100
        height: 50
        fit: contain
        transform:
          x: 0.5
          y: 0.5
          scale_x: 2
          scale_y: 0.5
      - type: video
        path: source.mp4
        width: 100
        height: 80
        fit: cover
        trim: [0.1, 0.2]
        transform: center
      - type: text
        width: 50
        height: 80
        transform: center
        text:
          - "one two three"
`))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := Compile(value, Options{})
	if err != nil {
		t.Fatal(err)
	}

	layers := plan.Scenes[0].Layers
	image := layers[0]
	if image.Width != 200 || image.Height != 25 || image.X != 60 || image.Y != 108 {
		t.Errorf("scaled image bounds = %#v", image)
	}

	video := layers[1]
	if video.Width != 100 || video.Height != 80 || video.Trim == nil {
		t.Errorf("video layout = %#v", video)
	}
	if video.Trim != nil && (video.Trim.Top != 0.1 || video.Trim.Bottom != 0.1 || video.Trim.Left != 0.2 || video.Trim.Right != 0.2) {
		t.Errorf("video trim = %#v", video.Trim)
	}

	text := layers[2]
	if text.WrapWidth != 50 || !strings.Contains(text.Spans[0].Content, "\n") {
		t.Errorf("text was not wrapped to its pixel width: %#v", text)
	}

	commands, err := plan.Commands()
	if err != nil {
		t.Fatal(err)
	}

	output := strings.Join(commands[0], " ")
	for _, expected := range []string{
		"scale=200:25",
		"crop=iw*(1-0.2-0.2)",
		"ih*(1-0.1-0.1)",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("command does not contain %q:\n%s", expected, output)
		}
	}
}
