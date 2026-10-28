package render

import (
	"strings"
	"testing"

	"github.com/ondics/yaml2video/src/project"
)

func TestCompileTextAndSubtitleBackgrounds(t *testing.T) {
	value, err := project.Parse([]byte(`
video:
  width: 320
  height: 240
  fps: 30
  background: black
defaults:
  text:
    font: Go
    size: 24
    color: white
    background:
      color: black
      opacity: 0.5
      padding: [8, 4]
  subtitle:
    font: Go
    size: 20
    color: white
    background:
      color: black
      opacity: 0.5
      padding: 6
scenes:
  - id: text-backgrounds
    duration: 2
    layers:
      - type: text
        background:
          color: blue
          opacity: 0.75
          padding: [3, 5]
        text:
          - content: Default background
          - content: Item background
            background:
              color: red
              opacity: 0.25
              padding: [1, 2]
    subtitles:
      - duration: 1
        text:
          - Subtitle background
`))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := Compile(value, Options{})
	if err != nil {
		t.Fatal(err)
	}

	textLayer := plan.Scenes[0].Layers[0]
	if textLayer.Background == nil {
		t.Fatal("layer text background was not compiled")
	}
	if textLayer.Background.PaddingX != 3 || textLayer.Background.PaddingY != 5 {
		t.Errorf("layer text padding = %#v", textLayer.Background)
	}

	textSpans := textLayer.Spans
	if textSpans[0].Background != nil {
		t.Errorf("first span unexpectedly has an item background: %#v", textSpans[0].Background)
	}
	if textSpans[1].Background == nil || textSpans[1].Background.PaddingX != 1 || textSpans[1].Background.PaddingY != 2 {
		t.Errorf("item background = %#v", textSpans[1].Background)
	}

	subtitle := plan.Scenes[0].Subtitles[0]
	if subtitle.Background == nil || subtitle.Background.PaddingX != 6 || subtitle.Background.PaddingY != 6 {
		t.Errorf("subtitle background = %#v", subtitle.Background)
	}

	commands, err := plan.Commands()
	if err != nil {
		t.Fatal(err)
	}

	command := strings.Join(commands[0], " ")
	for _, background := range []string{
		"drawbox=color=0x0000FF@0.75",
		"drawbox=color=0xFF0000@0.25",
	} {
		if !strings.Contains(command, background) {
			t.Errorf("text background rectangle %q is missing:\n%s", background, command)
		}
	}

	ass := plan.Scenes[0].ASS
	const subtitleBackgroundTags = `\3c&H00000000&\3a&H80&\xbord6\ybord6`
	if !strings.Contains(ass, subtitleBackgroundTags) {
		t.Errorf("subtitle ASS does not contain background tags %q:\n%s", subtitleBackgroundTags, ass)
	}
}
