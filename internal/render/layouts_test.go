package render

import (
	"os"
	"strings"
	"testing"
	"time"
)

// editFile replaces the first occurrence of find in a fixture file.
func editFile(t *testing.T, file, find, replace string) {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), find) {
		t.Fatalf("fixture %s lacks %q", file, find)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(raw), find, replace, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func layoutTrack(scene ScenePlan) *TrackPlan {
	for i := range scene.Tracks {
		if scene.Tracks[i].ID == "layout" {
			return &scene.Tracks[i]
		}
	}
	return nil
}

func clipByID(track *TrackPlan, id string) *ClipPlan {
	for i := range track.Clips {
		if track.Clips[i].ID == id {
			return &track.Clips[i]
		}
	}
	return nil
}

func TestNamedAndCustomLayouts(t *testing.T) {
	video, tmpl := example(t, "layouts")
	p, err := Load(video, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"intro", "slide", "stat", "slide-image-low", "slide", "quote"}
	for i, scene := range p.Scenes {
		if scene.Layout != want[i] {
			t.Errorf("scene %s layout=%s want %s", scene.ID, scene.Layout, want[i])
		}
	}

	intro := layoutTrack(p.Scenes[0])
	if intro == nil || len(p.Scenes[0].Layers) != 0 || p.Scenes[0].ASS != "" {
		t.Fatalf("custom intro should render only through its layout track")
	}
	if kinds := clipKinds(intro); kinds != "rect,image,text,text" {
		t.Errorf("intro elements in order: %s", kinds)
	}
	if got := spanText(intro.Clips[3].Layer); got != "Drei Übungen für zwischendurch" {
		t.Errorf("fallback not used: %q", got)
	}
	if got := spanText(clipByID(intro, "headline").Layer); got != "Beweg dich kurz!" {
		t.Errorf("$(title) = %q", got)
	}

	stat := layoutTrack(p.Scenes[2])
	if got := spanText(clipByID(stat, "number").Layer); got != "8" {
		t.Errorf("$(NUMBER) = %q", got)
	}
	if p.Scenes[2].Background != "#7DE2A8" || clipKinds(stat) != "text,text,rect,text,box,box" {
		t.Errorf("stat layout: background %s, elements %s", p.Scenes[2].Background, clipKinds(stat))
	}
	if fill := stat.Clips[5].Layer.Width; fill*2 != stat.Clips[4].Layer.Width {
		t.Errorf("progress for slide 2 of 4 should fill half: %d of %d", fill, stat.Clips[4].Layer.Width)
	}

	if classic := p.Scenes[3]; layoutTrack(classic) != nil || len(classic.Layers) != 3 || classic.Background != "#0C2A24" {
		t.Errorf("named classic layout: %+v", classic.Layers)
	}
	if rect := layoutTrack(p.Scenes[5]).Clips[0]; rect.Layer.Color != "#1C3B44" {
		t.Errorf("outro alias with $(ACCENT): %s", rect.Layer.Color)
	}

	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(flatten(commands), " ")
	for _, want := range []string{"scene-0000-layout-text-00.ass", "scene-0002-layout-text-00.ass", "scene-0002-layout-text-01.ass"} {
		if !strings.Contains(all, want) {
			t.Errorf("text runs should render at their stack position: missing %s", want)
		}
	}
}

func clipKinds(track *TrackPlan) string {
	kinds := make([]string, 0, len(track.Clips))
	for _, clip := range track.Clips {
		kinds = append(kinds, clip.Kind)
	}
	return strings.Join(kinds, ",")
}

func TestLayoutVariables(t *testing.T) {
	b := map[string]string{"NAME": "Ada", "price": "5"}
	for _, tc := range []struct{ in, want, err string }{
		{"Hi $(NAME)", "Hi Ada", ""},
		{"Hi ${NAME}", "Hi Ada", ""},
		{"$(MISSING|nobody) $$(NAME)", "nobody $(NAME)", ""},
		{"$(price)$$", "5$", ""},
		{"$(MISSING)", "", "unknown variable $(MISSING)"},
	} {
		got, err := bind(tc.in, b)
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%q: err %v want %s", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestLayoutErrorsAndOptional(t *testing.T) {
	t.Run("optional element without a value is skipped", func(t *testing.T) {
		video, tmpl := example(t, "layouts")
		editFile(t, video, "    logo: assets/gesund-am-arbeitsplatz-logo.png\n", "")
		p, err := Load(video, tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if kinds := clipKinds(layoutTrack(p.Scenes[0])); kinds != "rect,text,text" {
			t.Errorf("logo element should be skipped: %s", kinds)
		}
	})

	for _, tc := range []struct {
		name, find, replace, want string
		inTemplate                bool
	}{
		{"unknown layout", "layout: stat", "layout: stats", `unknown layout "stats"`, false},
		{"missing variable", `NUMBER: "8"`, `NUMBRE: "8"`, "unknown variable $(NUMBER)", false},
		{"classic needs fields", "layout: slide-image-low\n          image: assets/nacken-seitlich.png\n", "layout: slide-image-low\n", "needs the slide's image and text", false},
		{"classic layout for the wrong role", "outro: quote", "outro: slide-image-low", "classic slide layout and cannot be used for the outro", true},
		{"alias loop", "outro: quote", "outro: outro", "alias loop", true},
		{"duplicate element id", "        - type: text\n          id: quote", "        - type: rect\n          id: quote\n          width: 10\n          height: 10\n          color: \"#FFFFFF\"\n          placement: { anchor: center }\n        - type: text\n          id: quote", "duplicate element id", true},
		{"bad element field", "max_lines: 5", "max_lines: 5\n          colour: red", "unknown property colour", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			video, tmpl := example(t, "layouts")
			target := video
			if tc.inTemplate {
				target = tmpl
			}
			editFile(t, target, tc.find, tc.replace)
			if _, err := Load(video, tmpl); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

const targetingTracks = `
advanced:
    tracks:
        - id: count-up
          attach: stat
          clips:
              - type: element
                target: number
                start: 1s
                duration: 1s
                set: { text: "$(NUMBER)+", style: { color: "#FF3FD0" } }
                keyframes:
                    - { at: 0s, scale: 1.3, ease: back-out }
                    - { at: 0.5s, scale: 1 }
        - id: nod
          attach: slide
          clips:
              - type: element
                target: text
                start: 0.5s
                keyframes:
                    - { at: 0s, y: 30, ease: ease-out }
                    - { at: 0.4s, y: 0 }
              - type: element
                target: tip
                start: 2s
                duration: 0.5s
                set: { visible: false }
`

// Sequential set clips, as in a count-up, plus one that overlaps the third.
const sequentialSetClips = `
advanced:
    tracks:
        - id: count-up
          attach: stat
          clips:
              - { type: element, target: number, start: 0s, duration: 0.5s, set: { text: "1" } }
              - { type: element, target: number, start: 0.5s, duration: 0.5s, set: { text: "2" } }
              - { type: element, target: number, start: 1s, duration: 0.5s, set: { text: "3" } }
              - { type: element, target: number, start: 1.2s, duration: 0.4s, set: { style: { color: "#FF3FD0" } } }
`

func TestSequentialSetClipsDoNotMultiply(t *testing.T) {
	video, tmpl := example(t, "layouts")
	raw, _ := os.ReadFile(video)
	if err := os.WriteFile(video, append(raw, []byte(sequentialSetClips)...), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(video, tmpl)
	if err != nil {
		t.Fatal(err)
	}

	ms := time.Millisecond
	want := map[Window]string{
		{0, 500 * ms}:          "1",
		{500 * ms, 1000 * ms}:  "2",
		{1000 * ms, 1500 * ms}: "3",
		{1200 * ms, 1600 * ms}: "8", // the original, recolored
		{1200 * ms, 1500 * ms}: "3", // the overlap: "3", recolored
	}
	got := map[Window]string{}
	for _, clip := range layoutTrack(p.Scenes[2]).Clips {
		if clip.ID != "number" || clip.Layer.ShowOnly == nil {
			continue
		}
		if _, dup := got[*clip.Layer.ShowOnly]; dup {
			t.Errorf("duplicate variant for %v", *clip.Layer.ShowOnly)
		}
		got[*clip.Layer.ShowOnly] = spanText(clip.Layer)
	}
	if len(got) != len(want) {
		t.Fatalf("variants: got %v, want %v", got, want)
	}
	for window, text := range want {
		if got[window] != text {
			t.Errorf("variant %v: got %q, want %q", window, got[window], text)
		}
	}
}

func TestElementTargeting(t *testing.T) {
	video, tmpl := example(t, "layouts")
	raw, _ := os.ReadFile(video)
	if err := os.WriteFile(video, append(raw, []byte(targetingTracks)...), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(video, tmpl)
	if err != nil {
		t.Fatal(err)
	}

	// Custom element: the original hides during the window, a variant shows.
	stat := layoutTrack(p.Scenes[2])
	original, variant := stat.Clips[0], stat.Clips[1]
	window := Window{time.Second, 2 * time.Second}
	if original.ID != "number" || variant.ID != "number" || len(original.Layer.HideDuring) != 1 || original.Layer.HideDuring[0] != window {
		t.Fatalf("original: %+v", original.Layer.HideDuring)
	}
	if variant.Layer.ShowOnly == nil || *variant.Layer.ShowOnly != window || spanText(variant.Layer) != "8+" || variant.Layer.Color != "#FF3FD0" {
		t.Errorf("variant: %+v %q %s", variant.Layer.ShowOnly, spanText(variant.Layer), variant.Layer.Color)
	}
	if got := variant.Layer.Animation.Value("scale", time.Second, 0); got != 1.3 {
		t.Errorf("variant keyframes are relative to the clip start: scale at 1s = %g", got)
	}
	// Scene-relative ASS: the original pauses at 1 s, the variant starts there.
	doc := textRunDocument(p.Video, textRuns(stat.Clips)[0])
	// Animated text is sampled per frame (30 fps here).
	if !strings.Contains(doc, "Dialogue: 0,0:00:00.00,0:00:01.00,Advanced") || !strings.Contains(doc, "Dialogue: 0,0:00:01.00,0:00:01.03,Advanced") || !strings.Contains(doc, "Dialogue: 0,0:00:02.00,0:00:03.00,Advanced") {
		t.Errorf("window not reflected in ASS:\n%s", doc)
	}

	// Classic elements in every slide using a classic layout (and only those).
	for _, i := range []int{1, 3, 4} {
		text := findElement(p.Scenes[i], "text")
		if text == nil || text.Animation.Value("y", 500*time.Millisecond, 0) != 30 {
			t.Errorf("%s: text not animated from 0.5 s", p.Scenes[i].ID)
		}
		tip := findElement(p.Scenes[i], "tip")
		if tip == nil || len(tip.HideDuring) != 1 || strings.Count(p.Scenes[i].ASS, "Je Seite")+strings.Count(p.Scenes[i].ASS, "10-mal")+strings.Count(p.Scenes[i].ASS, "Füße") != 2 {
			t.Errorf("%s: hidden tip should split into two events:\n%s", p.Scenes[i].ID, p.Scenes[i].ASS)
		}
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ find, replace, want string }{
		{"target: number", "target: numbr", `target "numbr" matches no element`},
		{`set: { text: "$(NUMBER)+", style: { color: "#FF3FD0" } }`, "set: { colour: red }", "unknown property colour"},
		{"attach: stat", "attach: video", "element clips need a scene"},
	} {
		video, tmpl := example(t, "layouts")
		raw, _ := os.ReadFile(video)
		if err := os.WriteFile(video, append(raw, []byte(strings.Replace(targetingTracks, tc.find, tc.replace, 1))...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(video, tmpl); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.replace, err, tc.want)
		}
	}
}

func TestTextLayerKeepsExplicitLineBreaks(t *testing.T) {
	video := VideoSpec{Width: 1000, Height: 1000}
	el := obj{"placement": obj{"anchor": "center"}, "style": obj{"font_size": 50.0}, "max_width_ratio": 0.5}
	theme := obj{"text_color": "#FFFFFF"}

	for _, tc := range []struct{ value, want string }{
		{"This video\nis a YAML file.", "This video\nis a YAML file."},
		{"one\r\n\ntwo\n", "one\n\ntwo"},
		{"a long line that wraps around the box width", "a long line that wraps\naround the box width"},
	} {
		layer, err := textLayer(tc.value, "text", el, obj{}, theme, video)
		if err != nil {
			t.Fatal(err)
		}
		if got := layer.Spans[0].Content; got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.value, got, tc.want)
		}
	}

	el["max_lines"] = 1.0
	if _, err := textLayer("one\ntwo", "text", el, obj{}, theme, video); err == nil || !strings.Contains(err.Error(), "max_lines") {
		t.Errorf("explicit line breaks should count toward max_lines, got %v", err)
	}
}
