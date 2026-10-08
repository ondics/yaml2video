package render

import (
	"strings"
	"testing"
	"time"
)

func testV2Plan() *Plan {
	return &Plan{
		Video: VideoSpec{Width: 320, Height: 240, FPS: 30}, Duration: 3500 * time.Millisecond,
		Output: "video.mp4", Scenes: []ScenePlan{
			{ID: "one", Background: "black", Duration: 2 * time.Second, Output: "one.mkv", Layers: []LayerPlan{{Kind: "image", Path: "image.png", Fit: "cover", Width: 200, Height: 100, Opacity: 1, Effects: []EffectPlan{
				{Type: "pan_zoom", FromScale: 1, ToScale: 1.1, ToAnchor: "top-center"},
				{Type: "vignette", Color: "#000000", Opacity: .2},
				{Type: "fade", FadeIn: time.Second},
			}}}, Audio: []AudioLayerPlan{{Path: "effect.wav", Volume: .5, Offset: 500 * time.Millisecond, Duration: time.Second, FadeIn: 100 * time.Millisecond, FadeOut: 200 * time.Millisecond}}},
			{ID: "two", Background: "black", Start: 1500 * time.Millisecond, Duration: 2 * time.Second, Output: "two.mkv"},
		}, Transitions: []BoundaryTransition{{FromScene: 0, Type: "wipe-left", Duration: 500 * time.Millisecond}},
		Music: &MusicPlan{Path: "music.mp3", Volume: .3, FadeIn: 200 * time.Millisecond, FadeOut: 500 * time.Millisecond, Normalize: true, Ducking: &DuckingPlan{Enabled: true, Amount: .55, Attack: 150 * time.Millisecond, Release: 400 * time.Millisecond}},
	}
}

func TestV2GraphIncludesEffectsAudioAndTransition(t *testing.T) {
	p := testV2Plan()
	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}

	scene := strings.Join(commands[0], " ")
	final := strings.Join(commands[len(commands)-1], " ")
	for _, part := range []string{"zoompan", "vignette", "fade=", "alpha=1"} {
		if !strings.Contains(scene, part) {
			t.Errorf("scene missing %q: %s", part, scene)
		}
	}

	for _, part := range []string{"transition=wipeleft", "adelay", "delays=500", "afade", "loudnorm", "sidechaincompress", "asplit", "amix"} {
		if !strings.Contains(final, part) {
			t.Errorf("final missing %q: %s", part, final)
		}
	}
}

func TestContainedMediaFollowsEveryAnchor(t *testing.T) {
	for _, tc := range []struct{ anchor, x, y string }{
		{"top-left", "0", "0"}, {"top-center", "(ow-iw)/2", "0"}, {"top-right", "ow-iw", "0"},
		{"center-left", "0", "(oh-ih)/2"}, {"center", "(ow-iw)/2", "(oh-ih)/2"}, {"center-right", "ow-iw", "(oh-ih)/2"},
		{"bottom-left", "0", "oh-ih"}, {"bottom-center", "(ow-iw)/2", "oh-ih"}, {"bottom-right", "ow-iw", "oh-ih"},
	} {
		t.Run(tc.anchor, func(t *testing.T) {
			p := testV2Plan()
			layer := &p.Scenes[0].Layers[0]
			layer.Fit, layer.Anchor = "contain", tc.anchor
			commands, err := p.Commands()
			if err != nil {
				t.Fatal(err)
			}
			filter := strings.Join(commands[0], " ")
			if want := "pad=200:100:" + tc.x + ":" + tc.y + ":color=black@0"; !strings.Contains(filter, want) {
				t.Errorf("expected %s in %s", want, filter)
			}
			if !strings.Contains(filter, "format=rgba") {
				t.Error("padding must preserve transparency")
			}
		})
	}
}

func TestContainFitUsesEvenScaleWithinOddBounds(t *testing.T) {
	p := testV2Plan()
	p.Scenes[0].Layers[0].Fit = "contain"
	p.Scenes[0].Layers[0].Width = 259
	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(commands[0], " "), "force_divisible_by=2") {
		t.Fatal("contain scale must not round beyond the pad bounds")
	}
}

func TestSoundEffectsDoNotTriggerDucking(t *testing.T) {
	p := testV2Plan()
	p.Scenes[0].Audio[0].SoundEffect = true
	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Join(commands[len(commands)-1], " ")
	if strings.Contains(final, "sidechaincompress") || !strings.Contains(final, "effect.wav") {
		t.Fatalf("sound effect should mix without ducking: %s", final)
	}
}

func TestV2CutIsConcatWithoutOverlap(t *testing.T) {
	p := testV2Plan()
	p.Transitions = []BoundaryTransition{{FromScene: 0, Type: "cut"}}
	p.Scenes[1].Start = 2 * time.Second
	p.Duration = 4 * time.Second
	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Join(commands[len(commands)-1], " ")
	if !strings.Contains(final, "concat") || strings.Contains(final, "xfade") {
		t.Fatalf("cut should concatenate: %s", final)
	}
}

func TestV2RejectsUnsupportedCases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Plan)
		want   string
	}{
		{"effect", func(p *Plan) {
			p.Scenes[0].Layers[0].Effects = append(p.Scenes[0].Layers[0].Effects, EffectPlan{Type: "mystery"})
		}, "unsupported effect"},
		{"colored vignette", func(p *Plan) { p.Scenes[0].Layers[0].Effects[1].Color = "#FF0000" }, "colored vignette"},
		{"invalid panel bounds", func(p *Plan) {
			p.Scenes[0].Layers = append(p.Scenes[0].Layers, LayerPlan{Kind: "text", Effects: []EffectPlan{{Type: "glass_panel", Blur: 10}}})
		}, "positive width and height"},
		{"shape", func(p *Plan) { p.Scenes[0].Layers[0].Shape = "triangle" }, "media shape"},
		{"audio", func(p *Plan) { p.Scenes[0].Audio[0].Offset = 2 * time.Second }, "audio 0"},
		{"transition", func(p *Plan) { p.Transitions[0].Type = "unknown" }, "unsupported scene transition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testV2Plan()
			tc.change(p)
			_, err := p.Commands()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestV2TextEffectASS(t *testing.T) {
	scene := ScenePlan{Duration: 2 * time.Second, Layers: []LayerPlan{{Kind: "text", X: 10, Y: 10, Width: 100, Height: 40, Font: "Arial", FontSize: 24, Color: "0xFFFFFF", Opacity: 1, Spans: []TextSpan{{Content: "Hello"}}, Effects: []EffectPlan{
		{Type: "text_shadow", Color: "#000000", Opacity: .5, OffsetX: 1, OffsetY: 2, Blur: 2},
		{Type: "text_outline", Color: "#FF0000", Opacity: 1, Width: 2},
		{Type: "fade", FadeIn: 100 * time.Millisecond},
	}}}}
	doc := assDocument(320, 240, scene)
	for _, part := range []string{"\\xshad1\\yshad2", "\\3c&H000000FF&", "\\fad(100,0)"} {
		if !strings.Contains(doc, part) {
			t.Errorf("ASS missing %q: %s", part, doc)
		}
	}
}
