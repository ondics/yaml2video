package render

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Shapes can be placed on a text's measured glyphs: `cover` sizes a clip to
// the characters a text shows (a highlight), and `caret` moves a clip along
// a text as it types (a cursor). Both use the text's rest position, as libass
// lays it out with the font that draws it.

// charBox is where one character of a text layer is drawn, in frame pixels.
type charBox struct {
	X0, X1 float64
	Line   int
}

// lineBox is one line of a text layer; Top is the top of its line box, which
// is \fs tall.
type lineBox struct {
	X0, X1, Top float64
}

// textGeometry is the layout libass gives a text layer: its characters (line
// breaks excluded, as reveals count them) and its lines.
type textGeometry struct {
	chars []charBox
	lines []lineBox
	size  float64
	face  *fontFace
}

// spanText is a text layer's content, its spans joined.
func spanText(layer LayerPlan) string {
	var b strings.Builder
	for _, span := range layer.Spans {
		b.WriteString(span.Content)
	}
	return b.String()
}

// layerGeometry lays out a text layer as its ASS dialogue draws it: lines of
// \fs height stacked around the layer's middle, each aligned on the anchor x
// (see v2TextParts).
func layerGeometry(l LayerPlan, video VideoSpec) (textGeometry, error) {
	face := l.face
	if face == nil {
		weight := l.Weight
		if weight == 0 {
			weight = 400
			if len(l.Spans) > 0 && l.Spans[0].Bold {
				weight = 700
			}
		}
		var err error
		face, err = fontsOf(video).face(l.Font, weight, float64(l.FontSize))
		if err != nil {
			return textGeometry{}, err
		}
	}
	lines := strings.Split(spanText(l), "\n")
	size := float64(l.FontSize)
	g := textGeometry{size: size, face: face}
	top := float64(l.Y+l.Height/2) - float64(len(lines))*size/2
	for i, line := range lines {
		runes := []rune(line)
		x := face.offsets(runes, size, l.LetterSpacing, l.WordSpacing)
		width := face.lineWidth(runes, size, l.LetterSpacing, l.WordSpacing)
		start := float64(l.X+l.Width/2) - width/2
		switch l.Align {
		case "left":
			start = float64(l.X)
		case "right":
			start = float64(l.X+l.Width) - width
		}
		g.lines = append(g.lines, lineBox{start, start + width, top + float64(i)*size})
		for k := range runes {
			g.chars = append(g.chars, charBox{start + x[k], start + x[k+1], i})
		}
	}
	return g, nil
}

// textTarget is a text clip found by id, with its start on the timeline of
// the clips it was looked up in.
type textTarget struct {
	clip  *ClipPlan
	start time.Duration
}

// findText looks for the text clip id among clips and in their folders.
func findText(clips []ClipPlan, id string, offset time.Duration) (textTarget, bool) {
	for i := range clips {
		clip := &clips[i]
		if clip.ID == id && clip.Kind == "text" {
			return textTarget{clip, offset + clip.Start}, true
		}
		if clip.Folder != nil {
			if t, ok := findText(clip.Folder.Clips, id, offset+clip.Start); ok {
				return t, true
			}
		}
	}
	return textTarget{}, false
}

// attachToText places the clips of one timeline (a layout, track, or folder)
// that have cover or caret on the text they name, which must be on the same
// timeline or in a folder on it.
func attachToText(clips []ClipPlan, video VideoSpec) error {
	for i := range clips {
		clip := &clips[i]
		cover, caret := object(clip.def["cover"]), object(clip.def["caret"])
		spec, kind := cover, "cover"
		if len(caret) > 0 {
			spec, kind = caret, "caret"
		}
		if len(spec) == 0 {
			continue
		}
		label := kind
		if clip.ID != "" {
			label = fmt.Sprintf("%q", clip.ID)
		}
		if len(cover) > 0 && len(caret) > 0 {
			return fmt.Errorf("%s: a clip can't both cover a text and follow its caret", label)
		}
		target, ok := findText(clips, str(spec["text"]), 0)
		if !ok {
			return fmt.Errorf("%s: %s.text %q is no text in the same layout, track, or folder", label, kind, spec["text"])
		}
		var err error
		if kind == "cover" {
			err = coverText(clip, target, spec, video)
		} else {
			err = followCaret(clip, target, spec, video)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		fitAnimatedSize(&clip.Layer)
	}
	return nil
}

// coverText sizes a clip to the box around the characters its text shows
// (all, or those the text's match covers), narrowed by cover.match, plus
// padding. Spaces at the box's edges don't count.
func coverText(clip *ClipPlan, target textTarget, spec obj, video VideoSpec) error {
	text := target.clip.Layer
	g, err := layerGeometry(text, video)
	if err != nil {
		return err
	}
	shown := matchedChars(text)
	var narrowed []bool
	if spec["match"] != nil {
		re, err := bindPattern(spec["match"], clip.bindings)
		if err != nil {
			return fmt.Errorf("cover.match: %w", err)
		}
		narrowed = patternChars(text, re)
	}
	runes := []rune(strings.ReplaceAll(spanText(text), "\n", ""))
	box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for i, c := range g.chars {
		if runes[i] == ' ' || shown != nil && !shown[i] || narrowed != nil && !narrowed[i] {
			continue
		}
		top := g.lines[c.Line].Top
		box = [4]float64{math.Min(box[0], c.X0), math.Min(box[1], top), math.Max(box[2], c.X1), math.Max(box[3], top+g.size)}
	}
	if math.IsInf(box[0], 1) {
		return fmt.Errorf("cover: text %q shows no characters to cover", spec["text"])
	}
	px, py := num(spec["padding_x"]), num(spec["padding_y"])
	x0, y0 := math.Round(box[0]-px), math.Round(box[1]-py)
	clip.Layer.X, clip.Layer.Y = int(x0), int(y0)
	clip.Layer.Width = max(int(math.Round(box[2]+px)-x0), 1)
	clip.Layer.Height = max(int(math.Round(box[3]+py)-y0), 1)
	return nil
}

// followCaret moves a clip with the end of its text's typed characters
// (chars_end): its left edge after the last typed character, centered on that
// character's line, from frame to frame. Its size is its own width and
// height, or a character cell: the font's "0" by one line.
func followCaret(clip *ClipPlan, target textTarget, spec obj, video VideoSpec) error {
	anim := clip.Layer.Animation
	if anim.Has("x", "y") {
		return fmt.Errorf("a caret moves by itself; it can't have x or y keyframes")
	}
	text := target.clip.Layer
	g, err := layerGeometry(text, video)
	if err != nil {
		return err
	}
	w, h := clip.def["width"], clip.def["height"]
	clip.Layer.Width, clip.Layer.Height = integer(w), integer(h)
	if w == nil {
		zero := g.face.offsets([]rune("0"), g.size, 0, 0)
		clip.Layer.Width = max(int(math.Round(zero[1])), 1)
	}
	if h == nil {
		clip.Layer.Height = int(math.Round(g.size))
	}
	dx, dy := num(spec["offset_x"]), num(spec["offset_y"])
	position := func(count int) (int, int) {
		x, line := g.lines[0].X0, 0
		if count > 0 {
			c := g.chars[count-1]
			x, line = c.X1, c.Line
		}
		y := g.lines[line].Top + g.size/2 - float64(clip.Layer.Height)/2
		return int(math.Round(x + dx)), int(math.Round(y + dy))
	}

	r := defaultReveal
	if text.Reveal != nil {
		r = *text.Reveal
	}
	chars, _ := textCounts(text)
	shift := clip.Start - target.start // caret time → text time
	frame := time.Second / time.Duration(max(video.FPS, 1))
	var xs, ys []Keyframe
	for t := time.Duration(0); t <= clip.Duration; t += frame {
		x, y := position(revealCount(text, "chars_end", r.CharsEnd, chars, t+shift))
		if len(xs) == 0 {
			clip.Layer.X, clip.Layer.Y = x, y
		}
		x, y = x-clip.Layer.X, y-clip.Layer.Y
		if len(xs) == 0 || xs[len(xs)-1].Value != float64(x) || ys[len(ys)-1].Value != float64(y) {
			xs = append(xs, Keyframe{At: t, Value: float64(x), Ease: "step"})
			ys = append(ys, Keyframe{At: t, Value: float64(y), Ease: "step"})
		}
	}
	if len(xs) > 1 {
		if anim == nil {
			anim = &Animation{Props: map[string][]Keyframe{}}
		}
		anim.Props["x"], anim.Props["y"] = xs, ys
		clip.Layer.Animation = anim
	}
	return nil
}
