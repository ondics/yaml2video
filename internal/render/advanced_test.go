package render

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEasingEndpoints(t *testing.T) {
	for kind := range easings {
		if kind == "step" {
			if ease(kind, 0.99) != 0 {
				t.Errorf("step should hold until the next keyframe")
			}
			continue
		}
		if got := ease(kind, 0); math.Abs(got) > 1e-9 {
			t.Errorf("%s(0)=%g", kind, got)
		}
		if got := ease(kind, 1); math.Abs(got-1) > 1e-9 {
			t.Errorf("%s(1)=%g", kind, got)
		}
	}
	if ease("back-out", 0.5) <= 1 {
		t.Errorf("back-out should overshoot")
	}
}

func TestKeyframeValuesAndExpressions(t *testing.T) {
	raw := []any{
		obj{"at": "0.5s", "x": jsonNumber(100), "opacity": jsonNumber(0), "ease": "linear"},
		obj{"at": "-0.5s", "x": jsonNumber(-100), "opacity": jsonNumber(1)},
	}
	anim, err := parseKeyframes(raw, 2*time.Second, visualProps)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at   time.Duration
		want float64
	}{{0, 100}, {500 * time.Millisecond, 100}, {time.Second, 0}, {1500 * time.Millisecond, -100}, {2 * time.Second, -100}} {
		if got := anim.Value("x", tc.at, 7); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("x(%s)=%g want %g", tc.at, got, tc.want)
		}
	}
	if got := anim.Value("scale", time.Second, 1); got != 1 {
		t.Errorf("unanimated properties use the default, got %g", got)
	}
	expr := anim.Expr("x", "T", 0)
	for _, part := range []string{"if(lt(T,0.500000),100,", "if(lt(T,1.500000),", "(-100)"} {
		if !strings.Contains(expr, part) {
			t.Errorf("expression %q lacks %q", expr, part)
		}
	}
	if anim.Expr("rotation", "T", 0) != "0" {
		t.Errorf("unanimated expression should be the constant default")
	}

	if _, err := parseKeyframes([]any{obj{"at": "3s", "x": jsonNumber(1)}}, 2*time.Second, visualProps); err == nil {
		t.Error("keyframe after the owner's end must be rejected")
	}
	if _, err := parseKeyframes([]any{obj{"at": "0s", "volume": jsonNumber(1)}}, time.Second, visualProps); err == nil {
		t.Error("volume must not animate visual clips")
	}
}

func TestAdvancedExamplePlan(t *testing.T) {
	video, tmpl := example(t, "advanced")
	p, err := Load(video, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scenes) != 6 || p.Master == nil || p.Master.Loudness != -16 {
		t.Fatalf("scenes=%d master=%+v", len(p.Scenes), p.Master)
	}
	// Section layout, alias to a classic layout, named custom and classic layouts, outro alias.
	for i, want := range []string{"opener", "card", "stat", "slide-image-low", "card", "closing"} {
		if p.Scenes[i].Layout != want {
			t.Errorf("scene %s layout=%s want %s", p.Scenes[i].ID, p.Scenes[i].Layout, want)
		}
	}

	intro := p.Scenes[0]
	opener := layoutTrack(intro)
	if opener == nil || clipKinds(opener) != "circle,image,text,text" || trackIDs(intro) != "layout,intro-camera" {
		t.Fatalf("intro: %s / %s", clipKinds(opener), trackIDs(intro))
	}
	if headline := clipByID(opener, "headline"); headline == nil || !headline.Layer.Animation.Has("scale", "opacity") {
		t.Errorf("custom headline should carry its keyframes")
	}
	if got := spanText(opener.Clips[3].Layer); got != "Drei Übungen für zwischendurch" {
		t.Errorf("subtitle fallback: %q", got)
	}

	// Classic card slides: template animate, element-clip highlight, accent bar by layout name.
	schultern := p.Scenes[1]
	if !strings.Contains(schultern.ASS, "Style: Advanced") || !strings.Contains(schultern.ASS, `\move(`) {
		t.Errorf("animated classic text should use the advanced style and \\move segments")
	}
	if text := findElement(schultern, "text"); text == nil || len(text.HideDuring) != 1 {
		t.Errorf("highlight-text should modify the classic text")
	}

	// The custom stat slide: variables, fallbacks, optional image skipped, element clips via attach by layout.
	stat := layoutTrack(p.Scenes[2])
	if kinds := clipKinds(stat); kinds != "text,text,text,text,rect,text,text,box,box,effect" {
		t.Errorf("stat elements: %s", kinds)
	}
	if spanText(stat.Clips[0].Layer) != "GUT ZU WISSEN" || spanText(stat.Clips[3].Layer) != "Stunden" {
		t.Errorf("label fallback / UNIT variable not applied")
	}
	if number := stat.Clips[2]; number.ID != "number" || spanText(number.Layer) != "8+" || number.Layer.ShowOnly == nil {
		t.Errorf("count-up should add an 8+ variant: %+v", number.Layer.ShowOnly)
	}
	if len(p.Scenes[2].Audio) != 1 || !strings.HasSuffix(p.Scenes[2].Audio[0].Path, "pop.wav") {
		t.Errorf("layout audio element: %+v", p.Scenes[2].Audio)
	}

	// Element-only tracks (count-up, legs-motion) change layouts and add no track of their own.
	for i, want := range []string{
		"slide-counter,accent-bar",
		"layout,slide-counter",
		"slide-counter,label-badge,tip-badge",
		"slide-counter,accent-bar",
	} {
		scene := p.Scenes[i+1]
		if ids := trackIDs(scene); ids != want {
			t.Errorf("%s tracks=%s want %s", scene.ID, ids, want)
		}
		for _, track := range scene.Tracks {
			if track.ID == "slide-counter" && !strings.Contains(trackText(p, track), fmt.Sprintf("%d/4", i+1)) {
				t.Errorf("%s counter binding not resolved", scene.ID)
			}
		}
	}
	// The label badge exists only where the slide has a label, offset 0.3 s.
	if badge := p.Scenes[3].Tracks[1]; badge.ID != "label-badge" || badge.Clips[0].Start != 300*time.Millisecond {
		t.Errorf("label badge: %+v", badge)
	}
	if tip := findElement(p.Scenes[4], "tip"); tip == nil || len(tip.HideDuring) != 1 || tip.HideDuring[0] != (Window{0, time.Second}) {
		t.Errorf("beine tip should be hidden for its first second")
	}

	// Custom outro through the alias, with a ducking, keyframed audio clip.
	outro := p.Scenes[5]
	if cta := clipByID(layoutTrack(outro), "cta"); cta == nil || spanText(cta.Layer) != "Speichern und später wiederholen" {
		t.Errorf("closing layout cta")
	}
	if a := outro.Audio[0]; a.SoundEffect || a.VolumeAnimation == nil {
		t.Errorf("outro pop should duck and carry volume keyframes: %+v", a)
	}

	// watermark is disabled by the video.
	if len(p.Tracks) != 1 || p.Tracks[0].ID != "grade" || p.Tracks[0].Clips[0].Duration != p.Duration {
		t.Errorf("video tracks: %+v", p.Tracks)
	}

	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(flatten(commands), " ")
	for _, want := range []string{"perspective", "geq", "blend", "vignette", "scene-0002-layout-text-00.ass", "scene-0003-track-07-text-00.ass", "loudnorm", "sidechaincompress"} {
		if !strings.Contains(all, want) {
			t.Errorf("commands lack %q", want)
		}
	}
}

func TestAdvancedModeIsOptional(t *testing.T) {
	for _, kind := range []string{"simple", "normal", "complex"} {
		video, tmpl := example(t, kind)
		p, err := Load(video, tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Tracks) != 0 || p.Master != nil || len(p.Audio) != 0 {
			t.Errorf("%s: unexpected advanced plan", kind)
		}
		for _, scene := range p.Scenes {
			if len(scene.Tracks) != 0 || strings.Contains(scene.ASS, "Advanced") {
				t.Errorf("%s/%s: advanced output without an advanced block", kind, scene.ID)
			}
		}
	}
}

func TestAdvancedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, find, replace, want string
		inTemplate                bool
	}{
		{"unknown preset", "keyframes: pop-in\n              - type: text", "keyframes: missing\n              - type: text", "unknown keyframe preset", true},
		{"unknown binding", `"$(index)/$(total)"`, `"$(nope)"`, "unknown variable $(nope)", true},
		{"clip too long", "start: 0.5s\n                duration: 2s", "start: 0.5s\n                duration: 9s", "exceeds its owner", true},
		{"unknown scene", "attach: exercises-beine", "attach: exercises-hals", "matches no scene", false},
		{"bad disable", "disable_tracks: [watermark]", "disable_tracks: [watermark, nope]", "unknown track", false},
		{"element target missing", "target: caption", "target: captoin", `target "captoin" matches no element`, true},
		{"bad clip field", "corner_radius: 4", "corner_radius: 4\n                bogus: 1", "unknown property bogus", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			video, tmpl := example(t, "advanced")
			target := video
			if tc.inTemplate {
				target = tmpl
			}
			raw, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), tc.find) {
				t.Fatalf("fixture lacks %q", tc.find)
			}
			if err := os.WriteFile(target, []byte(strings.Replace(string(raw), tc.find, tc.replace, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(video, tmpl); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTransitions(t *testing.T) {
	video, tmpl := example(t, "advanced")
	p, err := Load(video, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind, xfade string
		d           time.Duration
		effects     int
	}{
		{"flash-cut", "", 0, 1},                                          // custom, no base: a cut with a flash
		{"slide-left", "slideleft", 400 * time.Millisecond, 0},           // rotating list, first entry
		{"smooth-up", "smoothup", 400 * time.Millisecond, 0},             // second entry
		{"iris", "custom", 600 * time.Millisecond, 0},                    // video scene override
		{"smooth-up → zoom-blur", "smoothup", 400 * time.Millisecond, 0}, // list wraps; outro adds its in side
	}
	if len(p.Transitions) != len(want) {
		t.Fatalf("transitions=%d", len(p.Transitions))
	}
	for i, w := range want {
		tr := p.Transitions[i]
		if tr.Type != w.kind || tr.xfade() != w.xfade || tr.Duration != w.d || len(tr.Effects) != w.effects {
			t.Errorf("transition %d: %+v", i, tr)
		}
	}
	if expr := p.Transitions[3].Expr; !strings.Contains(expr, "(1-P)*hypot") {
		t.Errorf("progress should be rewritten for xfade: %s", expr)
	}
	// The flash is centered on the cut.
	if flash := p.Transitions[0].Effects[0]; flash.Start != p.Scenes[1].Start-200*time.Millisecond {
		t.Errorf("flash at %s, cut at %s", flash.Start, p.Scenes[1].Start)
	}
	outro := p.Scenes[len(p.Scenes)-1]
	if last := outro.Tracks[len(outro.Tracks)-1]; last.ID != "transition-in" || last.Clips[0].Start != 0 || last.Clips[0].Duration != 300*time.Millisecond {
		t.Errorf("outro in side: %+v", last)
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}
}

func TestTransitionSidesAndDefaults(t *testing.T) {
	edit := func(t *testing.T, file, find, replace string) {
		t.Helper()
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), find) {
			t.Fatalf("fixture lacks %q", find)
		}
		if err := os.WriteFile(file, []byte(strings.Replace(string(raw), find, replace, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("out transition covers both sides", func(t *testing.T) {
		video, tmpl := example(t, "advanced")
		edit(t, tmpl, "transition_out: { type: flash-cut }", "transition_out: { type: zoom-blur }")
		p, err := Load(video, tmpl)
		if err != nil {
			t.Fatal(err)
		}
		intro, first := p.Scenes[0], p.Scenes[1]
		out := intro.Tracks[len(intro.Tracks)-1]
		if out.ID != "transition-out" || out.Clips[0].Start != intro.Duration-300*time.Millisecond {
			t.Errorf("out side: %+v", out)
		}
		if in := first.Tracks[len(first.Tracks)-1]; in.ID != "transition-in" || in.Clips[0].Start != 0 {
			t.Errorf("in side should default to the same transition: %+v", in)
		}
	})

	t.Run("no transition is a cut", func(t *testing.T) {
		video, tmpl := example(t, "advanced")
		edit(t, tmpl, "transition_out: { type: flash-cut }\n", "\n")
		p, err := Load(video, tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if p.Scenes[1].Start != p.Scenes[0].Duration || p.Transitions[0].FromScene != 1 {
			t.Errorf("intro should cut: %+v", p.Transitions[0])
		}
	})

	for _, tc := range []struct{ name, find, replace, want string }{
		{"unknown", "{ type: flash-cut }", "{ type: nope }", "unknown transition"},
		{"built-in without duration", "{ type: flash-cut }", "{ type: fade-black }", "needs a duration"},
		{"base and expr", "expr: \"if(", "base: fade\n            expr: \"if(", "base or expr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			video, tmpl := example(t, "advanced")
			edit(t, tmpl, tc.find, tc.replace)
			if _, err := Load(video, tmpl); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTrackOffset(t *testing.T) {
	load := func(t *testing.T, find, replace string) (*Plan, error) {
		t.Helper()
		video, tmpl := example(t, "advanced")
		raw, err := os.ReadFile(tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), find) {
			t.Fatalf("fixture lacks %q", find)
		}
		if err := os.WriteFile(tmpl, []byte(strings.Replace(string(raw), find, replace, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(video, tmpl)
	}
	badge := func(p *Plan) ClipPlan {
		for _, scene := range p.Scenes {
			for _, track := range scene.Tracks {
				if track.ID == "tip-badge" {
					return track.Clips[0]
				}
			}
		}
		t.Fatal("tip-badge track missing")
		return ClipPlan{}
	}
	const attach = "attach: slide-image-low\n"

	// The 3 s scene's badge clip starts 0.5 s into the track.
	for _, tc := range []struct {
		offset string
		want   time.Duration
	}{{"0.4s", 900 * time.Millisecond}, {`"-2.5s"`, time.Second}} {
		p, err := load(t, attach, attach+"          offset: "+tc.offset+"\n")
		if err != nil {
			t.Fatal(err)
		}
		if got := badge(p); got.Start != tc.want || got.Duration != 2*time.Second {
			t.Errorf("offset %s: clip %s+%s, want start %s", tc.offset, got.Start, got.Duration, tc.want)
		}
		if !strings.Contains(trackText(p, p.Scenes[3].Tracks[2]), v2ASSTime(tc.want)) {
			t.Errorf("offset %s: track text not shifted", tc.offset)
		}
	}

	if _, err := load(t, attach, attach+"          offset: 0.6s\n"); err == nil || !strings.Contains(err.Error(), "exceeds its owner") {
		t.Errorf("clip beyond the offset window: %v", err)
	}
	if _, err := load(t, attach, attach+"          offset: 3s\n"); err == nil || !strings.Contains(err.Error(), "before the end") {
		t.Errorf("offset at the owner's end: %v", err)
	}
}

func TestRepeatedAudioInputsAreSplit(t *testing.T) {
	dir := t.TempDir()
	sfx := filepath.Join(dir, "pop.wav")
	scene := func(i int) ScenePlan {
		return ScenePlan{ID: "s", Duration: 2 * time.Second, Background: "#000000",
			Output: filepath.Join(dir, "s.mkv"), Start: time.Duration(i) * 2 * time.Second,
			Audio: []AudioLayerPlan{{Path: sfx, Volume: 1, Duration: time.Second, SoundEffect: true}}}
	}
	p := &Plan{Video: VideoSpec{Width: 64, Height: 64, FPS: 30}, Scenes: []ScenePlan{scene(0), scene(1)},
		Duration: 4 * time.Second, Output: filepath.Join(dir, "out.mp4"), WorkDir: dir}
	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Join(commands[len(commands)-1], " ")
	if strings.Count(final, "-i "+sfx) != 1 || !strings.Contains(final, "asplit=2") {
		t.Errorf("repeated sound should be decoded once and split: %s", final)
	}
}

// trackText concatenates the ASS documents of a track's text runs.
func trackText(p *Plan, track TrackPlan) string {
	var docs []string
	for _, run := range textRuns(track.Clips) {
		docs = append(docs, textRunDocument(p.Video, run))
	}
	return strings.Join(docs, "\n")
}

func findElement(scene ScenePlan, element string) *LayerPlan {
	for i := range scene.Layers {
		if scene.Layers[i].Element == element {
			return &scene.Layers[i]
		}
	}
	return nil
}

func trackIDs(scene ScenePlan) string {
	ids := make([]string, 0, len(scene.Tracks))
	for _, track := range scene.Tracks {
		ids = append(ids, track.ID)
	}
	return strings.Join(ids, ",")
}

func flatten(commands [][]string) []string {
	var out []string
	for _, command := range commands {
		out = append(out, command...)
	}
	return out
}
