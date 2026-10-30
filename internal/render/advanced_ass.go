package render

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Animated text is sampled once per frame. Within each segment libass
// interpolates linearly (\move and \t), so any easing curve is followed exactly
// at frame times, and a reveal changes on the frame it should.
func frameStep(video VideoSpec) time.Duration {
	if video.FPS <= 0 {
		return 50 * time.Millisecond
	}
	return time.Second / time.Duration(video.FPS)
}

// textRuns groups consecutive text clips: each run renders through one ASS
// document at its place in the stack.
func textRuns(clips []ClipPlan) [][]ClipPlan {
	var runs [][]ClipPlan
	var run []ClipPlan
	for _, clip := range clips {
		if clip.Kind == "text" {
			run = append(run, clip)
			continue
		}
		if len(run) > 0 {
			runs = append(runs, run)
			run = nil
		}
	}
	if len(run) > 0 {
		runs = append(runs, run)
	}
	return runs
}

func runASSName(track TrackPlan, run int) string {
	return fmt.Sprintf("%s-text-%02d.ass", track.ASSPrefix, run)
}

// textRunDocument renders a run of text clips, timed on their owner's timeline.
func textRunDocument(video VideoSpec, run []ClipPlan) string {
	var b strings.Builder
	writeV2ASSHeader(&b, video, true)
	for _, clip := range run {
		if clip.Layer.Animation != nil {
			writeAnimatedText(&b, clip.Layer, clip.Layer.Align, clip.Start, clip.Start+clip.Duration, frameStep(video))
		} else {
			writeStaticText(&b, clip.Layer, clip.Layer.Align, "Advanced", clip.Start, clip.Start+clip.Duration)
		}
	}
	return b.String()
}

type textState struct {
	x, y, scaleX, scaleY, rotation, opacity float64
}

func writeAnimatedText(b *strings.Builder, layer LayerPlan, align string, start, end, step time.Duration) {
	alignment, x, y, style, tags, fadeInMS, fadeOutMS, value := v2TextParts(layer, align)
	fadeIn, fadeOut := time.Duration(fadeInMS)*time.Millisecond, time.Duration(fadeOutMS)*time.Millisecond
	outline, shadow := 1.0, 1.0
	for _, fx := range layer.Effects {
		switch fx.Type {
		case "text_glow", "text_outline":
			outline = fx.Opacity
		case "text_shadow":
			shadow = fx.Opacity
		}
	}

	anim, length := layer.Animation, end-start
	sample := func(t time.Duration) textState {
		scale := anim.Value("scale", t, 1)
		opacity := anim.Value("opacity", t, 1)
		// Fades are folded into opacity because \fad would restart on every segment.
		if fadeIn > 0 && t < fadeIn {
			opacity *= float64(t) / float64(fadeIn)
		}
		if fadeOut > 0 && t > length-fadeOut {
			opacity *= float64(length-t) / float64(fadeOut)
		}
		return textState{
			x:        float64(x) + anim.Value("x", t, 0),
			y:        float64(y) + anim.Value("y", t, 0),
			scaleX:   100 * scale * anim.Value("scale_x", t, 1),
			scaleY:   100 * scale * anim.Value("scale_y", t, 1),
			rotation: -anim.Value("rotation", t, 0), // ASS rotates counterclockwise
			opacity:  math.Max(0, math.Min(1, opacity)),
		}
	}
	transform := func(s textState) string {
		return fmt.Sprintf("\\fscx%.2f\\fscy%.2f\\frz%.2f", s.scaleX, s.scaleY, s.rotation)
	}
	alpha := func(s textState) string {
		return fmt.Sprintf("\\1a%s\\3a%s\\4a%s", v2ASSAlpha(s.opacity), v2ASSAlpha(s.opacity*outline), v2ASSAlpha(s.opacity*shadow))
	}

	// One event per frame; runs of identical, motionless frames merge into one.
	type event struct {
		t0, t1 time.Duration
		text   string
		still  bool
	}
	var events []event
	for _, span := range layer.visible(start, end) {
		for t0 := span.Start - start; t0 < span.End-start; t0 += step {
			t1 := min(t0+step, span.End-start)
			s0, s1 := sample(t0), sample(t1)
			body := value
			if show := revealAt(layer, t0); show != nil {
				// Text after hidden characters restores the animated alpha.
				body = textBody(layer, show, fmt.Sprintf("{%s\\t(%s)}", alpha(s0), alpha(s1)))
			}
			text := fmt.Sprintf("{\\an%d\\move(%.1f,%.1f,%.1f,%.1f)%s%s%s%s\\t(%s%s)}%s",
				alignment, s0.x, s0.y, s1.x, s1.y, style, tags, transform(s0), alpha(s0), transform(s1), alpha(s1), body)
			still := s0 == s1
			if n := len(events); n > 0 && still && events[n-1].still && events[n-1].t1 == t0 && events[n-1].text == text {
				events[n-1].t1 = t1
				continue
			}
			events = append(events, event{t0, t1, text, still})
		}
	}
	for _, e := range events {
		fmt.Fprintf(b, "Dialogue: 0,%s,%s,Advanced,,0,0,0,,%s\n", v2ASSTime(start+e.t0), v2ASSTime(start+e.t1), e.text)
	}
}

// visible returns the parts of [start, end) where the layer is shown.
func (l LayerPlan) visible(start, end time.Duration) []Window {
	spans := []Window{{start, end}}
	if l.ShowOnly != nil {
		spans = []Window{{max(start, l.ShowOnly.Start), min(end, l.ShowOnly.End)}}
		if spans[0].End <= spans[0].Start {
			return nil
		}
	}
	for _, hide := range l.HideDuring {
		var kept []Window
		for _, s := range spans {
			if hide.End <= s.Start || hide.Start >= s.End {
				kept = append(kept, s)
				continue
			}
			if hide.Start > s.Start {
				kept = append(kept, Window{s.Start, hide.Start})
			}
			if hide.End < s.End {
				kept = append(kept, Window{hide.End, s.End})
			}
		}
		spans = kept
	}
	return spans
}

// enableExpr is the FFmpeg timeline expression for the layer's windows, or ""
// when it is always shown.
func (l LayerPlan) enableExpr() string {
	var parts []string
	if l.ShowOnly != nil {
		parts = append(parts, fmt.Sprintf("gte(t,%s)*lt(t,%s)", ffTime(l.ShowOnly.Start), ffTime(l.ShowOnly.End)))
	}
	for _, hide := range l.HideDuring {
		parts = append(parts, fmt.Sprintf("(1-gte(t,%s)*lt(t,%s))", ffTime(hide.Start), ffTime(hide.End)))
	}
	return strings.Join(parts, "*")
}
