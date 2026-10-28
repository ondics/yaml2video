package render

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A template whose "card" layout groups a background, code text, a cursor, and
// a highlight in one folder, with a layout track that edits the folder's children.
const folderTemplate = `$schema: x
version: 2
template_id: folders
description: folders
format: { aspect_ratio: "9:16", width: 1080, height: 1920, fps: 30 }
defaults:
  theme: { background: "#000000", text_color: "#FFFFFF", accent_color: "#D7FF3E" }
  timing: { intro_duration: 2s, slide_duration: 2s, outro_duration: 2s }
composition:
  sections:
    - { id: s, role: slide, source: content.slides, repeat: true }
    - { id: o, role: outro, source: content.outro, layout: plain }
layout:
  intro: card
  slide: card
  outro: card
  plain: [{ type: text, text: $(cta), placement: { anchor: center } }]
  card:
    duration: 4s
    transition_out: { type: fade, duration: 0.5s }
    elements:
      - type: folder
        id: code
        start: 1s
        keyframes: [{ at: 0s, scale: 0.5 }, { at: 1s, scale: 1 }]
        effects: [{ type: shake, amount: 4 }]
        clips:
          - { type: rect, id: bg, width: 800, height: 400, color: "#141418", placement: { anchor: center } }
          - type: folder
            id: inner
            start: 0.5s
            clips:
              - type: text
                id: code-text
                text: "a: 1\n  b: 2"
                placement: { anchor: center }
                style: { font_family: "Go Mono", font_size: 40, align: left }
                reveal: { chars_end: "50%" }
              - { type: rect, id: cursor, width: 10, height: 40, color: "#D7FF3E", placement: { anchor: center, offset_x: 100 } }
              - { type: audio, path: $(SOUND), start: 0.25s, duration: 0.5s, optional: true }
          - { type: rect, id: hl, width: 300, height: 40, color: "#D7FF3E", placement: { anchor: center, offset_y: 100 } }
    tracks:
      - id: edits
        clips:
          - { type: element, target: cursor, start: 2s, keyframes: [{ at: 0s, x: 0 }, { at: 1s, x: 200 }] }
          - { type: element, target: code-text, start: 2s, keyframes: [{ at: 0s, chars_end: 5 }, { at: 1s, chars_end: "100%" }] }
          - { type: element, target: hl, start: 2.5s, duration: 0.5s, set: { color: "#FF0000" } }
`

const folderVideo = `$schema: x
version: 2
template: folders
metadata: { title: folders, language: en-US }
content:
  slides:
    - { id: one }
    - { id: two, duration: 3s, transition_out: { type: dissolve, duration: 0.25s } }
  outro: { cta: done }
`

func loadFolders(t *testing.T, video, template string) *Plan {
	t.Helper()
	p, err := FromBytes([]byte(video), []byte(template), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// findClip searches a clip list and nested folders by id.
func findClip(clips []ClipPlan, id string) *ClipPlan {
	for i := range clips {
		if clips[i].ID == id {
			return &clips[i]
		}
		if clips[i].Folder != nil {
			if found := findClip(clips[i].Folder.Clips, id); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestFolderGroupsChildren(t *testing.T) {
	p := loadFolders(t, folderVideo, folderTemplate)
	layout := layoutTrack(p.Scenes[0])
	code := findClip(layout.Clips, "code")
	if code == nil || code.Kind != "folder" || code.Start != time.Second || code.Duration != 3*time.Second {
		t.Fatalf("folder: %+v", code)
	}
	// Keyframes scale the group around its children's center (the 800×400 card).
	if code.Layer.Pivot == nil || *code.Layer.Pivot != [2]float64{540, 960} || code.Layer.Animation.Value("scale", 0, 1) != 0.5 {
		t.Errorf("pivot %v, scale %g", code.Layer.Pivot, code.Layer.Animation.Value("scale", 0, 1))
	}
	// The folder's effects run last, over all of its children.
	children := code.Folder.Clips
	if last := children[len(children)-1]; last.Kind != "effect" || last.Adjustments[0].Type != "shake" || last.Duration != 3*time.Second {
		t.Errorf("folder effect: %+v", last)
	}
	// Children are timed from their folder's start, through nesting.
	inner := findClip(children, "inner")
	if inner.Start != 500*time.Millisecond || inner.Duration != 2500*time.Millisecond {
		t.Errorf("inner folder %s for %s", inner.Start, inner.Duration)
	}
	if text := findClip(inner.Folder.Clips, "code-text"); text.Start != 0 || text.Duration != 2500*time.Millisecond {
		t.Errorf("nested text %s for %s", text.Start, text.Duration)
	}

	// Every folder's text gets its own ASS document: the inner folder of each
	// card scene, besides the outro's layout text.
	var names []string
	for _, track := range p.allTracks() {
		if len(textRuns(track.Clips)) > 0 {
			names = append(names, runASSName(track, 0))
		}
	}
	if strings.Join(names, " ") != "folder-002-text-00.ass folder-004-text-00.ass scene-0002-layout-text-00.ass" {
		t.Errorf("ASS documents: %v", names)
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}
}

func TestFolderCanvasCoversWhatChildrenReach(t *testing.T) {
	p := loadFolders(t, folderVideo, folderTemplate)
	code := findClip(layoutTrack(p.Scenes[0]).Clips, "code")
	c := *code.Folder.Canvas
	// The 800×400 card is centered at (540, 960); the canvas is just around it.
	if c.X > 140-canvasMargin || c.Y > 760-canvasMargin || c.X+c.W < 940+canvasMargin || c.Y+c.H < 1160+canvasMargin || c.W > 1080 || c.H > 700 {
		t.Errorf("canvas %+v should hug the card", c)
	}
	if code.Layer.X != c.X || code.Layer.Width != c.W {
		t.Errorf("folder layer %d+%d should be its canvas", code.Layer.X, code.Layer.Width)
	}

	// A child flying in from 700 px below reaches past the frame's bottom edge.
	flying := strings.Replace(folderTemplate, "{ type: rect, id: bg, width: 800, height: 400, color: \"#141418\", placement: { anchor: center } }",
		"{ type: rect, id: bg, width: 800, height: 400, color: \"#141418\", placement: { anchor: bottom-center, offset_y: -100 }, keyframes: [{ at: 0s, y: 700 }, { at: 1s, y: 0 }] }", 1)
	p = loadFolders(t, folderVideo, flying)
	c = *findClip(layoutTrack(p.Scenes[0]).Clips, "code").Folder.Canvas
	if c.Y+c.H < 1920+600 {
		t.Errorf("canvas %+v should reach below the frame", c)
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}
}

func TestElementClipsReachIntoFolders(t *testing.T) {
	p := loadFolders(t, folderVideo, folderTemplate)
	layout := layoutTrack(p.Scenes[0])

	// The cursor lives 1.5 s into the scene (folder 1 s + inner 0.5 s); a clip
	// at 2 s scene time animates it from 0.5 s on its own timeline.
	cursor := findClip(layout.Clips, "cursor")
	if got := cursor.Layer.Animation.Value("x", 500*time.Millisecond, -1); got != 0 {
		t.Errorf("cursor x at its 0.5 s = %g, want 0", got)
	}
	if got := cursor.Layer.Animation.Value("x", 1500*time.Millisecond, -1); got != 200 {
		t.Errorf("cursor x at its 1.5 s = %g, want 200", got)
	}

	// The reveal keyframes reach the nested text the same way.
	text := findClip(layout.Clips, "code-text")
	// Counts and percentages mix: 5 of 10 characters, halfway to 100 %.
	if got := text.Layer.Animation.ValueOf("chars_end", time.Second, 0, 10); got != 7.5 {
		t.Errorf("chars_end at 1 s of the text = %g, want 7.5", got)
	}

	// A set clip on a folder child shows a variant in the child's folder, with
	// windows on the folder's timeline (2.5 s scene time = 1.5 s folder time).
	code := findClip(layout.Clips, "code")
	var hl []ClipPlan
	for _, clip := range code.Folder.Clips {
		if clip.ID == "hl" {
			hl = append(hl, clip)
		}
	}
	window := Window{1500 * time.Millisecond, 2 * time.Second}
	if len(hl) != 2 || hl[0].Layer.HideDuring[0] != window || *hl[1].Layer.ShowOnly != window || hl[1].Layer.Color != "#FF0000" {
		t.Errorf("highlight variants: %+v", hl)
	}
}

func TestFolderAudioIsPlacedOnTheSceneTimeline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pop.wav"), []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	video := strings.Replace(folderVideo, "- { id: one }", "- { id: one, variables: { SOUND: pop.wav } }", 1)
	p, err := FromBytes([]byte(video), []byte(folderTemplate), dir)
	if err != nil {
		t.Fatal(err)
	}
	// Folder 1 s + inner folder 0.5 s + clip 0.25 s; only scene one has a sound.
	audio := p.Scenes[0].Audio
	if len(audio) != 1 || audio[0].Offset != 1750*time.Millisecond || len(p.Scenes[1].Audio) != 0 {
		t.Errorf("folder audio: %+v", audio)
	}
}

func TestLayoutSceneDefaults(t *testing.T) {
	p := loadFolders(t, folderVideo, folderTemplate)
	one, two := p.Scenes[0], p.Scenes[1]
	// The layout's duration applies unless the item sets one.
	if one.Duration != 4*time.Second || two.Duration != 3*time.Second {
		t.Errorf("durations %s, %s", one.Duration, two.Duration)
	}
	// The layout's transition, unless the item overrides it.
	if len(p.Transitions) != 2 || p.Transitions[0].Type != "fade" || p.Transitions[1].Type != "dissolve" {
		t.Errorf("transitions: %+v", p.Transitions)
	}
	// Layout tracks attach to the layout.
	if cursor := findClip(layoutTrack(one).Clips, "cursor"); cursor.Layer.Animation == nil {
		t.Error("layout track did not reach the scene")
	}

	for _, tc := range []struct{ find, replace, want string }{
		{"  outro: { cta: done }", "  outro: { cta: done, transition_out: { type: fade, duration: 0.5s } }", "last scene has no transition_out"},
		{"- { id: one }", "- { id: one, transition_in: { type: fade, duration: 0.5s } }", "first scene has no transition_in"},
	} {
		if _, err := FromBytes([]byte(strings.Replace(folderVideo, tc.find, tc.replace, 1)), []byte(folderTemplate), t.TempDir()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.replace, err, tc.want)
		}
	}
	attached := strings.Replace(folderTemplate, "      - id: edits\n", "      - id: edits\n        attach: slide\n", 1)
	if _, err := FromBytes([]byte(folderVideo), []byte(attached), t.TempDir()); err == nil || !strings.Contains(err.Error(), "remove attach") {
		t.Errorf("layout track with attach: %v", err)
	}
}

func TestRevealKeepsLayout(t *testing.T) {
	layer := LayerPlan{Kind: "text", Spans: []TextSpan{{Content: "abcd\n  efgh"}}} // 10 characters, 2 lines
	// reveal builds a plan from chars_start, chars_end, lines_start, lines_end
	// as YAML gives them (counts or "%" strings); nil keeps the default.
	reveal := func(values ...any) *RevealPlan {
		r := defaultReveal
		for i, dst := range []*RevealValue{&r.CharsStart, &r.CharsEnd, &r.LinesStart, &r.LinesEnd} {
			if values[i] == nil {
				continue
			}
			if n, ok := values[i].(int); ok {
				values[i] = float64(n)
			}
			v, rel, err := revealValue(values[i])
			if err != nil {
				t.Fatal(err)
			}
			*dst = RevealValue{v, rel}
		}
		return &r
	}
	at := func(r *RevealPlan) string {
		layer.Reveal = r
		show := revealAt(layer, 0)
		if show == nil {
			return "all"
		}
		var b strings.Builder
		char, line := 0, 0
		for _, ch := range layer.Spans[0].Content {
			if ch == '\n' {
				b.WriteByte('|')
				line++
				continue
			}
			if show(char, line) {
				b.WriteRune(ch)
			} else {
				b.WriteByte('.')
			}
			char++
		}
		return b.String()
	}
	for _, tc := range []struct {
		r    *RevealPlan
		want string
	}{
		{reveal(nil, nil, nil, nil), "all"},
		{reveal(0, "100%", 0, 2), "all"},
		{reveal(5, nil, nil, nil), "....|. efgh"},       // the first 5 characters hidden
		{reveal("50%", nil, nil, nil), "....|. efgh"},   // the same as a percentage
		{reveal(nil, 5, nil, nil), "abcd| ....."},       // the characters from the 6th on hidden
		{reveal(2, 7, nil, nil), "..cd|  e..."},         // characters 2–6
		{reveal("25%", "75%", nil, nil), "..cd|  e..."}, // 2.5 and 7.5: whole characters only
		{reveal(nil, 3.9, nil, nil), "abc.|......"},     // 3.9 characters: 3
		{reveal(nil, 99, nil, nil), "all"},              // counts above the total show everything
		{reveal(nil, nil, 1, nil), "....|  efgh"},       // the first line hidden
		{reveal(nil, nil, nil, 1), "abcd|......"},       // the last line hidden
		{reveal(nil, 6, 1, nil), "....|  ...."},         // both: characters 0–5 on line 2
	} {
		if got := at(tc.r); got != tc.want {
			t.Errorf("%+v: got %q, want %q", *tc.r, got, tc.want)
		}
	}
	for _, bad := range []any{-1.0, "x", "-5%", "5"} {
		if _, _, err := revealValue(bad); err == nil {
			t.Errorf("reveal value %v: want an error", bad)
		}
	}

	// Hidden characters are transparent, not removed: the visible ones stay put.
	layer.Reveal = reveal(3, nil, nil, nil)
	body := textBody(layer, revealAt(layer, 0), "{\\1a&H00&}")
	if body != "{\\alpha&HFF&}abc{\\1a&H00&}d\\N\\h\\hefgh" {
		t.Errorf("body %q", body)
	}

	// Reveal keyframes type the text frame by frame.
	layer.Reveal = nil
	layer.Animation = &Animation{Props: map[string][]Keyframe{"chars_end": {{At: 0, Value: 0}, {At: time.Second, Value: 1, Relative: true}}}}
	var b strings.Builder
	writeAnimatedText(&b, layer, "left", 0, time.Second, time.Second/10)
	if n := strings.Count(b.String(), "Dialogue:"); n != 10 {
		t.Errorf("want one event per frame, got %d", n)
	}
	if !strings.Contains(b.String(), "}a{\\alpha&HFF&}bcd") {
		t.Errorf("typing frame missing:\n%s", b.String())
	}
}

func TestTextMatchAndReplace(t *testing.T) {
	template := strings.Replace(folderTemplate, `  plain: [{ type: text, text: $(cta), placement: { anchor: center } }]`, `  plain:
    - { type: text, id: keys, text: $(cta), match: '([a-z]+):', placement: { anchor: center }, max_width_ratio: 1 }
    - { type: text, id: strings, text: $(cta), match: '$(STR)', reveal: { chars_end: 14 }, placement: { anchor: center }, max_width_ratio: 1 }
    - { type: text, id: blocks, text: "$(cta) ", replace: { pattern: '[^\n]', with: "█" }, placement: { anchor: center }, max_width_ratio: 1 }`, 1)
	video := strings.Replace(folderVideo, "outro: { cta: done }", `outro:
    cta: |-
      a: "x"
        bb: "yz"
    variables: { STR: '"[a-z]+"' }`, 1)
	p := loadFolders(t, video, template)
	layout := layoutTrack(p.Scenes[2]).Clips
	visible := func(id string) string {
		layer := findClip(layout, id).Layer
		show := revealAt(layer, 0)
		var b strings.Builder
		char := 0
		for _, r := range spanText(layer) {
			switch {
			case r == '\n':
				b.WriteByte('|')
				continue
			case show == nil || show(char, 0):
				b.WriteRune(r)
			default:
				b.WriteByte('.')
			}
			char++
		}
		return b.String()
	}
	// The first group of '([a-z]+):' leaves the colon out.
	if got := visible("keys"); got != "a.....|..bb......" {
		t.Errorf("keys: %q", got)
	}
	// A match combines with the reveal: characters 14 on are hidden.
	if got := visible("strings"); got != "...\"x\"|......\"y.." {
		t.Errorf("strings: %q", got)
	}
	// Every character becomes a block; line breaks stay, so the grid matches.
	if got := spanText(findClip(layout, "blocks").Layer); got != "██████\n███████████" {
		t.Errorf("blocks: %q", got)
	}
}

func TestRevealKeyframesNeedText(t *testing.T) {
	broken := strings.Replace(folderTemplate, "target: cursor, start: 2s, keyframes: [{ at: 0s, x: 0 }", "target: cursor, start: 2s, keyframes: [{ at: 0s, x: 0, chars_end: 0 }", 1)
	if _, err := FromBytes([]byte(folderVideo), []byte(broken), t.TempDir()); err == nil || !strings.Contains(err.Error(), "need a text element") {
		t.Errorf("got %v", err)
	}
}

func TestSizeKeyframes(t *testing.T) {
	sized := strings.Replace(folderTemplate, `{ type: rect, id: hl, width: 300, height: 40, color: "#D7FF3E", placement: { anchor: center, offset_y: 100 } }`,
		`{ type: rect, id: hl, width: 300, height: 40, corner_radius: 20, color: "#D7FF3E", placement: { anchor: center, offset_y: 100 },
             keyframes: [{ at: 0s, height: 40, ease: back-out }, { at: 0.5s, height: 201 }] }`, 1)
	p := loadFolders(t, folderVideo, sized)
	hl := findClip(layoutTrack(p.Scenes[0]).Clips, "hl").Layer
	// Laid out at the largest height plus back-out headroom, an even step
	// taller, around the same center; the width stays.
	if hl.Width != 300 || hl.Height != 40+178 || hl.Y+hl.Height/2 != 960+100 || hl.X != 390 {
		t.Errorf("layer %d,%d %dx%d", hl.X, hl.Y, hl.Width, hl.Height)
	}
	if mask := shapeMask(hl); !strings.Contains(mask, "hypot") || !strings.Contains(mask, "st(2,max(min(20,min(ld(0),ld(1))),0))") {
		t.Errorf("mask %s", mask)
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}

	for _, broken := range []string{
		strings.Replace(folderTemplate, "reveal: { chars_end: \"50%\" }", "keyframes: [{ at: 0s, width: 10 }]", 1),
		strings.Replace(sized, "height: 201", "height: -1", 1),
	} {
		if _, err := FromBytes([]byte(folderVideo), []byte(broken), t.TempDir()); err == nil {
			t.Error("expected an error")
		}
	}
}

// A layout whose highlight covers a string of a text and whose cursor follows
// another text as it types; fonts fall back to Go Mono in tests.
const attachTemplate = `$schema: x
version: 2
template_id: folders
description: attach
format: { aspect_ratio: "9:16", width: 1080, height: 1920, fps: 10 }
defaults:
  theme: { background: "#000000", text_color: "#FFFFFF", accent_color: "#D7FF3E" }
  timing: { intro_duration: 2s, slide_duration: 2s, outro_duration: 2s }
composition:
  sections:
    - { id: s, role: slide, source: content.slides, repeat: true }
    - { id: o, role: outro, source: content.outro, layout: plain }
layout:
  intro: card
  slide: card
  outro: card
  plain: [{ type: text, text: $(cta), placement: { anchor: center } }]
  card:
    elements:
      - type: folder
        id: editor
        clips:
          - { type: rect, id: hl, color: "#D7FF3E", corner_radius: 8, cover: { text: code, match: '"value"', padding_x: 6, padding_y: 3 } }
          - type: text
            id: code
            text: "key: \"value\"\nnext: 2"
            placement: { anchor: center-left, offset_x: 100 }
            style: { font_family: "Go Mono", font_size: 50, align: left }
            keyframes: [{ at: 0s, chars_end: 0 }, { at: 1.2s, chars_end: 12 }, { at: 1.3s, chars_end: 13 }]
          - { type: rect, id: cursor, color: "#FF0000", start: 0.5s, caret: { text: code } }
`

func TestCoverAndCaret(t *testing.T) {
	p := loadFolders(t, folderVideo, attachTemplate)
	clips := layoutTrack(p.Scenes[0]).Clips
	code := findClip(clips, "code").Layer
	face, err := fontsOf(p.Video).face("Go Mono", 400, 50)
	if err != nil {
		t.Fatal(err)
	}
	cell := face.offsets([]rune("0"), 50, 0, 0)[1]
	top := float64(code.Y+code.Height/2) - 50 // two lines of 50 px around the middle

	// The highlight covers `"value"` (characters 5–11) plus padding.
	hl := findClip(clips, "hl").Layer
	x0 := math.Round(100 + 5*cell - 6)
	if hl.X != int(x0) || hl.Width != int(math.Round(100+12*cell+6)-x0) || hl.Y != int(math.Round(top-3)) || hl.Height != 56 {
		t.Errorf("highlight %d,%d %dx%d, want x %g w %g", hl.X, hl.Y, hl.Width, hl.Height, x0, 7*cell+12)
	}

	// The cursor is a character cell after the typed text: it starts 0.5 s
	// into the text's typing (5 characters in), then moves on with it.
	cursor := findClip(clips, "cursor").Layer
	if cursor.Width != int(math.Round(cell)) || cursor.Height != 50 {
		t.Errorf("cursor %dx%d", cursor.Width, cursor.Height)
	}
	at := func(sec float64) (float64, float64) {
		d := time.Duration(sec * float64(time.Second))
		return float64(cursor.X) + cursor.Animation.Value("x", d, 0), float64(cursor.Y) + cursor.Animation.Value("y", d, 0)
	}
	if x, y := at(0); x != math.Round(100+5*cell) || y != math.Round(top) {
		t.Errorf("cursor starts at %g,%g", x, y)
	}
	if x, _ := at(0.55); x != math.Round(100+10*cell) {
		t.Errorf("cursor at 0.55 s: %g", x)
	}
	// After the first line it waits at its end, then jumps to the next line.
	if x, y := at(0.75); x != math.Round(100+12*cell) || y != math.Round(top) {
		t.Errorf("cursor at the line's end: %g,%g", x, y)
	}
	if x, y := at(0.85); x != math.Round(100+cell) || y != math.Round(top+50) {
		t.Errorf("cursor on the next line: %g,%g", x, y)
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ from, to, message string }{
		{"cover: { text: code,", "cover: { text: nope,", `"nope" is no text`},
		{"caret: { text: code } }", "caret: { text: code }, keyframes: [{ at: 0s, x: 1 }] }", "can't have x or y"},
		{"caret: { text: code } }", "caret: { text: code }, cover: { text: code } }", "both cover"},
	} {
		broken := strings.Replace(attachTemplate, tc.from, tc.to, 1)
		if _, err := FromBytes([]byte(folderVideo), []byte(broken), t.TempDir()); err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: got %v", tc.message, err)
		}
	}
}

func TestTextWrapsAtMeasuredWidths(t *testing.T) {
	video := VideoSpec{Width: 1000, Height: 1000}
	el := obj{"placement": obj{"anchor": "center"}, "style": obj{"font_size": 50.0, "font_family": "Go"}, "max_width_ratio": 0.5}
	// Narrow letters fit where as many wide ones wrap.
	narrow, err := textLayer("iiiii iiiii iiiii iiiii", "text", el, obj{}, obj{"text_color": "#FFFFFF"}, video)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := textLayer("WWWWW WWWWW WWWWW WWWWW", "text", el, obj{}, obj{"text_color": "#FFFFFF"}, video)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(spanText(narrow), "\n") != 0 || strings.Count(spanText(wide), "\n") == 0 {
		t.Errorf("narrow %q, wide %q", spanText(narrow), spanText(wide))
	}
	if narrow.Font != "Go" || narrow.face == nil {
		t.Errorf("font %q", narrow.Font)
	}
}

func TestTextKeepsIndentationAndSpacing(t *testing.T) {
	el := obj{"placement": obj{"anchor": "center"}, "style": obj{"font_size": 40.0, "word_spacing": 12.0}, "max_width_ratio": 0.5}
	layer, err := textLayer("key:\n    nested:  two", "text", el, obj{}, obj{"text_color": "#FFFFFF"}, VideoSpec{Width: 1000, Height: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if got := layer.Spans[0].Content; got != "key:\n    nested:  two" {
		t.Errorf("content %q", got)
	}
	if got := textBody(layer, nil, ""); got != "key:\\N\\h\\h\\h\\hnested:{\\fsp12} {\\fsp0}\\htwo" {
		t.Errorf("body %q", got)
	}
}
