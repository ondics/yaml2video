package render

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// animationMargin pads transform canvases so FFmpeg's edge clamping in
// perspective only ever repeats transparent pixels.
const animationMargin = 8

var cameraProps = []string{"x", "y", "scale", "scale_x", "scale_y", "rotation"}

// applyTracks composites tracks, and each track's clips, in order. Clip times
// are on the stream's own timeline (a scene, or the final video). A run of
// consecutive text clips renders through one ASS document at its position.
func applyTracks(base *ffmpeg.Stream, plan *Plan, tracks []TrackPlan) *ffmpeg.Stream {
	ordinal := 0
	return composite(base, plan, tracks, frameSpace(plan.Video), false, &ordinal)
}

// space is the area a stream draws, in frame coordinates: the frame, or a
// folder's canvas. Layers are positioned in frame coordinates and drawn
// relative to their space.
//
// A stream may hold only part of its owner's timeline (a segment of the
// output): its first frame is frame first of the owner, and frames (0 for
// the rest of the timeline) is how many it holds. Its timestamps stay the
// owner's.
type space struct {
	Rect
	fps           int
	first, frames int
}

func frameSpace(video VideoSpec) space {
	return space{Rect: Rect{0, 0, video.Width, video.Height}, fps: video.FPS}
}

// clipFrames returns the frames [from, to) of s's owner a clip shows in.
func (s space) clipFrames(clip ClipPlan) (from, to int) {
	fps := float64(s.fps)
	from = int(math.Ceil(clip.Start.Seconds()*fps - 1e-6))
	to = int(math.Floor((clip.Start+clip.Duration).Seconds()*fps+1e-6)) + 1
	return from, to
}

// outside reports whether a clip shows in none of the stream's frames.
func (s space) outside(clip ClipPlan) bool {
	from, to := s.clipFrames(clip)
	return to <= s.first || (s.frames > 0 && from >= s.first+s.frames)
}

// skipped is how many frames of a clip's own source end before the stream's
// first frame: the source starts at the last frame shown at or before it.
func (s space) skipped(clip ClipPlan) int {
	return max(int(math.Floor(float64(s.first)-clip.Start.Seconds()*float64(s.fps)+1e-6)), 0)
}

// local moves a layer into the space's own coordinates.
func (s space) local(l LayerPlan) LayerPlan {
	l.X, l.Y = l.X-s.X, l.Y-s.Y
	if l.Pivot != nil {
		pivot := [2]float64{l.Pivot[0] - float64(s.X), l.Pivot[1] - float64(s.Y)}
		l.Pivot = &pivot
	}
	return l
}

// composite is applyTracks for an opaque frame or, with canvas, for a
// folder's transparent canvas, where every step keeps the alpha channel.
// ordinal counts overlay sources across nested folders.
func composite(base *ffmpeg.Stream, plan *Plan, tracks []TrackPlan, s space, canvas bool, ordinal *int) *ffmpeg.Stream {
	for _, track := range tracks {
		run := 0
		for i := 0; i < len(track.Clips); i++ {
			clip := track.Clips[i]
			clip.Layer = s.local(clip.Layer)
			if clip.Kind != "text" && s.outside(clip) {
				if clip.Kind == "image" || clip.Kind == "rect" || clip.Kind == "circle" {
					*ordinal++
				}
				continue
			}
			switch clip.Kind {
			case "image", "rect", "circle":
				*ordinal++
				base = overlayClip(base, plan, s, clip, *ordinal, canvas)
			case "folder":
				base = overlayFolder(base, plan, s, clip, canvas, ordinal)
			case "box":
				base = base.Filter("drawbox", nil, canvasBox(ffmpeg.KwArgs{
					"x": clip.Layer.X, "y": clip.Layer.Y, "w": clip.Layer.Width, "h": clip.Layer.Height,
					"color": withOpacity(clip.Layer.Color, clip.Layer.Opacity), "t": "fill", "enable": clipEnable(clip),
				}, canvas))
			case "effect":
				base = applyEffectClip(base, s, clip, canvas)
			case "text":
				shown := false
				for ; i < len(track.Clips) && track.Clips[i].Kind == "text"; i++ {
					text := track.Clips[i]
					if s.outside(text) {
						continue
					}
					shown = true
					text.Layer = s.local(text.Layer)
					base = textClipPanel(base, text, canvas)
				}
				i--
				if shown {
					args := plan.assArgs(filepath.Join(plan.WorkDir, "ass", runASSName(track, run)))
					if canvas {
						args["alpha"] = 1
					}
					base = base.Filter(ffmpegFilterASS, nil, args)
				}
				run++
			}
		}
	}
	return base
}

// canvasBox makes a filled drawbox opaque on a transparent canvas, where a
// blended box would leave the canvas's zero alpha untouched.
func canvasBox(args ffmpeg.KwArgs, canvas bool) ffmpeg.KwArgs {
	if canvas {
		args["replace"] = 1
	}
	return args
}

// overlayArgs adds the canvas pixel format to overlay arguments, so overlays
// onto a folder canvas keep its alpha and full chroma.
func overlayArgs(args ffmpeg.KwArgs, canvas bool) ffmpeg.KwArgs {
	if canvas {
		args["format"] = "yuv444"
	}
	return args
}

// overlayFolder renders a folder's children onto its transparent canvas (an
// area that may reach past the frame) with the folder's own timeline,
// transforms the canvas as one layer (its keyframes turn around the folder's
// pivot), and overlays it. clip.Layer is already in s's coordinates.
func overlayFolder(base *ffmpeg.Stream, plan *Plan, s space, clip ClipPlan, canvas bool, ordinal *int) *ffmpeg.Stream {
	*ordinal++
	length := sourceLength(clip.Duration, *ordinal)
	// The canvas starts with the stream: inner's first frame is the folder's
	// first frame in it.
	skip := s.skipped(clip)
	inner := space{Rect: *clip.Folder.Canvas, fps: s.fps, first: skip}
	if s.frames > 0 {
		inner.frames = s.frames + 1 // the folder may start between frames
	}
	// The format must be part of the lavfi source: as a separate input, the
	// transparent color would be negotiated to a format without alpha first.
	source := fmt.Sprintf("color=c=black@0:s=%dx%d:r=%d:d=%s,format=yuva444p", inner.W, inner.H, s.fps, ffTime(length))
	group := skipFrames(ffmpeg.Input(source, ffmpeg.KwArgs{"f": "lavfi"}), skip)
	group = composite(group, plan, []TrackPlan{*clip.Folder}, inner, true, ordinal)

	layer := clip.Layer
	if layer.Opacity != 1 {
		group = group.ColorChannelMixer(ffmpeg.KwArgs{"aa": layer.Opacity})
	}
	group, x, y := animateMedia(group, s, layer, skip)
	if clip.Start > 0 {
		group = group.Filter("setpts", ffmpeg.Args{fmt.Sprintf("PTS+%s/TB", ffTime(clip.Start))})
	}
	args := ffmpeg.KwArgs{"x": x, "y": y}
	if enable := layer.enableExpr(); enable != "" {
		args["enable"] = enable
	}
	return base.Overlay(group, eofActionPass, overlayArgs(args, canvas))
}

// clipEnable is the timeline expression for a clip's window and the layer's
// own show/hide windows.
func clipEnable(clip ClipPlan) string {
	enable := clipWindow(clip)
	if extra := clip.Layer.enableExpr(); extra != "" {
		enable += "*" + extra
	}
	return enable
}

// sourceLength is the length of a clip's generated source. ffmpeg-go merges
// graph nodes with identical content, so each source is a distinct number of
// microseconds (its ordinal) shorter than the clip. Shorter, not longer: a
// clip ending on a frame boundary would otherwise get one extra frame, one
// more than text, whose subtitle events end exactly on time.
func sourceLength(d time.Duration, ordinal int) time.Duration {
	return max(d-time.Duration(ordinal)*time.Microsecond, time.Microsecond)
}

// overlayClip composites one visual clip, whose layer is in s's coordinates;
// see sourceLength for its length.
func overlayClip(base *ffmpeg.Stream, plan *Plan, s space, clip ClipPlan, ordinal int, canvas bool) *ffmpeg.Stream {
	layer := clip.Layer
	length := sourceLength(clip.Duration, ordinal)
	skip := s.skipped(clip)
	var input *ffmpeg.Stream
	if clip.Kind == "image" {
		input = mediaLayerStream(plan, ScenePlan{Duration: length}, layer, skip)
	} else {
		input = shapeStream(plan, layer, length, skip)
	}

	input, x, y := animateMedia(input, s, layer, skip)
	if clip.Start > 0 {
		input = input.Filter("setpts", ffmpeg.Args{fmt.Sprintf("PTS+%s/TB", ffTime(clip.Start))})
	}
	args := ffmpeg.KwArgs{"x": x, "y": y}
	if enable := layer.enableExpr(); enable != "" {
		args["enable"] = enable
	}
	return base.Overlay(input, eofActionPass, overlayArgs(args, canvas))
}

// shapeStream is a shape's source, without its first skip frames.
func shapeStream(plan *Plan, layer LayerPlan, d time.Duration, skip int) *ffmpeg.Stream {
	source := fmt.Sprintf("color=c=%s:s=%dx%d:r=%d:d=%s", layer.Color, layer.Width, layer.Height, plan.Video.FPS, ffTime(d))
	input := skipFrames(ffmpeg.Input(source, ffmpeg.KwArgs{"f": "lavfi"}), skip).Filter("format", ffmpeg.Args{"rgba"})
	if layer.Opacity != 1 {
		input = input.ColorChannelMixer(ffmpeg.KwArgs{"aa": layer.Opacity})
	}
	if masked(layer) {
		input = maskShape(input, layer)
	}
	return input
}

// animateMedia applies keyframes to a prepared media stream whose own timeline
// starts at the clip start, less its first skip frames. It returns the stream
// and its overlay position in s (the layer is in s's coordinates).
func animateMedia(input *ffmpeg.Stream, s space, layer LayerPlan, skip int) (*ffmpeg.Stream, int, int) {
	anim := layer.Animation
	if anim == nil {
		return input, layer.X, layer.Y
	}
	// Keyframed opacity, and scales that reach 0 (perspective keeps a
	// one-pixel sliver of a layer scaled to nothing), become an alpha factor.
	if collapses := anim.reaches0("scale", "scale_x", "scale_y"); anim.Has("opacity") || collapses {
		factor := fmt.Sprintf("clip(%s,0,1)", anim.Expr("opacity", "T", 1))
		if collapses {
			s := anim.Expr("scale", "T", 1)
			factor += fmt.Sprintf("*gt(%s*%s,0.002)*gt(%s*%s,0.002)", s, anim.Expr("scale_x", "T", 1), s, anim.Expr("scale_y", "T", 1))
		}
		input = scaleAlpha(input, factor, layer.Width, layer.Height)
	}
	if !anim.Has(cameraProps...) {
		return input, layer.X, layer.Y
	}

	// Moves, scales, and rotations: place the layer on a transparent canvas
	// that covers the layer and where it moves to, then move the canvas
	// corners with perspective. Its subpixel sampling keeps slow moves smooth;
	// overlay positions are whole pixels (even ones on yuv420p frames).
	input = input.Filter("format", ffmpeg.Args{"yuva444p"})
	pivot := layerPivot(layer)
	layer.Pivot = &pivot
	if crop, ok := visibleSource(s, layer); ok && crop != (Rect{layer.X, layer.Y, layer.Width, layer.Height}) {
		// Only what reaches s in some frame is transformed.
		input = input.Filter("crop", nil, ffmpeg.KwArgs{"x": crop.X - layer.X, "y": crop.Y - layer.Y, "w": crop.W, "h": crop.H})
		layer.X, layer.Y, layer.Width, layer.Height = crop.X, crop.Y, crop.W, crop.H
	}
	left, top, cw, ch := transformCanvas(s, layer)
	ox, oy := layer.X-left, layer.Y-top

	t := perspectiveTime(s.fps, skip)
	input = input.Filter("pad", nil, ffmpeg.KwArgs{"w": cw, "h": ch, "x": ox, "y": oy, "color": "black@0"}).
		Filter("perspective", nil, transformCorners(anim, t, float64(cw), float64(ch), pivot[0]-float64(left), pivot[1]-float64(top)))
	return input, left, top
}

// layerPivot is the point (in s) a layer scales and rotates around.
func layerPivot(layer LayerPlan) [2]float64 {
	if layer.Pivot != nil {
		return *layer.Pivot
	}
	return [2]float64{float64(layer.X) + float64(layer.Width)/2, float64(layer.Y) + float64(layer.Height)/2}
}

// transform is the move, scale, and rotation of a layer in one frame, the
// one transformCorners has perspective apply.
type transform struct {
	px, py, cx, cy, sx, sy, sin, cos float64
}

func (t transform) apply(x, y float64) (float64, float64) {
	dx, dy := x-t.px, y-t.py
	return t.cx + t.sx*dx*t.cos - t.sy*dy*t.sin, t.cy + t.sx*dx*t.sin + t.sy*dy*t.cos
}

func (t transform) invert(x, y float64) (float64, float64) {
	dx, dy := x-t.cx, y-t.cy
	return t.px + (dx*t.cos+dy*t.sin)/t.sx, t.py + (dy*t.cos-dx*t.sin)/t.sy
}

// layerTransforms returns the transform of each frame perspective renders,
// through the frame after the last keyframe; later frames repeat it.
func layerTransforms(s space, layer LayerPlan) []transform {
	anim := layer.Animation
	pivot := layerPivot(layer)
	frames := int(math.Ceil(anim.End().Seconds()*float64(s.fps))) + 1
	transforms := make([]transform, 0, frames+1)
	for n := 0; n <= frames; n++ {
		t := time.Duration(n) * time.Second / time.Duration(s.fps)
		scale := anim.Value("scale", t, 1)
		sin, cos := math.Sincos(anim.Value("rotation", t, 0) * math.Pi / 180)
		transforms = append(transforms, transform{
			px: pivot[0], py: pivot[1],
			cx: pivot[0] + anim.Value("x", t, 0), cy: pivot[1] + anim.Value("y", t, 0),
			sx: math.Max(scale*anim.Value("scale_x", t, 1), 0.001), sy: math.Max(scale*anim.Value("scale_y", t, 1), 0.001),
			sin: sin, cos: cos,
		})
	}
	return transforms
}

// visibleSource returns the part of a layer (in s) that reaches s in some
// frame, with animationMargin for interpolation, or false if none does.
// Large layers, such as folder canvases that reach past the frame, often
// show only part of themselves.
func visibleSource(s space, layer LayerPlan) (Rect, bool) {
	box := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, t := range layerTransforms(s, layer) {
		for _, corner := range [][2]float64{{0, 0}, {float64(s.W), 0}, {0, float64(s.H)}, {float64(s.W), float64(s.H)}} {
			x, y := t.invert(corner[0], corner[1])
			box = [4]float64{math.Min(box[0], x), math.Min(box[1], y), math.Max(box[2], x), math.Max(box[3], y)}
		}
	}
	x0 := max(int(math.Floor(box[0]))-animationMargin, layer.X)
	y0 := max(int(math.Floor(box[1]))-animationMargin, layer.Y)
	x1 := min(int(math.Ceil(box[2]))+animationMargin, layer.X+layer.Width)
	y1 := min(int(math.Ceil(box[3]))+animationMargin, layer.Y+layer.Height)
	if x0 >= x1 || y0 >= y1 {
		return Rect{}, false
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}, true
}

// transformCanvas returns the canvas (left, top, width, height in s) for a
// transformed layer: its rest box and the part of s it covers in any frame,
// plus animationMargin. perspective works on every canvas pixel in every
// frame, so a canvas that only covers the layer's path costs a fraction of
// one that covers all of s.
func transformCanvas(s space, layer LayerPlan) (left, top, width, height int) {
	x0, y0 := float64(layer.X), float64(layer.Y)
	x1, y1 := x0+float64(layer.Width), y0+float64(layer.Height)
	path := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, t := range layerTransforms(s, layer) {
		for _, corner := range [][2]float64{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
			x, y := t.apply(corner[0], corner[1])
			path = [4]float64{math.Min(path[0], x), math.Min(path[1], y), math.Max(path[2], x), math.Max(path[3], y)}
		}
	}
	// Only the part inside s is visible.
	path = [4]float64{math.Max(path[0], 0), math.Max(path[1], 0), math.Min(path[2], float64(s.W)), math.Min(path[3], float64(s.H))}
	if path[0] < path[2] && path[1] < path[3] {
		x0, y0 = math.Min(x0, path[0]), math.Min(y0, path[1])
		x1, y1 = math.Max(x1, path[2]), math.Max(y1, path[3])
	}
	// Even offsets and sizes keep the canvas on the chroma grid of subsampled
	// (yuv420p) frames it is overlaid on.
	left, top = (int(math.Floor(x0))-animationMargin)&^1, (int(math.Floor(y0))-animationMargin)&^1
	width = (int(math.Ceil(x1)) + animationMargin - left + 1) &^ 1
	height = (int(math.Ceil(y1)) + animationMargin - top + 1) &^ 1
	return left, top, width, height
}

// perspectiveTime is the time of the frame perspective renders, in seconds on
// the timeline of a stream that begins skip frames in: perspective's frame
// number "in" counts from 1.
func perspectiveTime(fps, skip int) string {
	return fmt.Sprintf("(%s/%d)", perspectiveFrame(skip), fps)
}

// perspectiveFrame is the number, from 0, of the frame perspective renders,
// in a stream that begins skip frames in.
func perspectiveFrame(skip int) string {
	if skip == 0 {
		return "(in-1)"
	}
	return fmt.Sprintf("(in-1+%d)", skip)
}

// skipFrames drops a stream's first frames, keeping its timestamps.
func skipFrames(s *ffmpeg.Stream, frames int) *ffmpeg.Stream {
	if frames == 0 {
		return s
	}
	return s.Filter("trim", nil, ffmpeg.KwArgs{"start_frame": frames})
}

// transformCorners maps the corners of a cw×ch canvas through translate, scale,
// and clockwise rotation around (px, py), as perspective destination points.
func transformCorners(anim *Animation, t string, cw, ch, px, py float64) ffmpeg.KwArgs {
	sx := fmt.Sprintf("max(%s*%s,0.001)", anim.Expr("scale", t, 1), anim.Expr("scale_x", t, 1))
	sy := fmt.Sprintf("max(%s*%s,0.001)", anim.Expr("scale", t, 1), anim.Expr("scale_y", t, 1))
	r := fmt.Sprintf("(%s*PI/180)", anim.Expr("rotation", t, 0))
	cx := fmt.Sprintf("(%s+%s)", num6(px), anim.Expr("x", t, 0))
	cy := fmt.Sprintf("(%s+%s)", num6(py), anim.Expr("y", t, 0))

	// Cubic keeps a slowly moving layer equally sharp at every subpixel
	// offset; linear softens it at half pixels, so its detail pulses.
	args := ffmpeg.KwArgs{"sense": "destination", "eval": "frame", "interpolation": "cubic"}
	for i, corner := range [][2]float64{{0, 0}, {cw, 0}, {0, ch}, {cw, ch}} {
		dx, dy := num6(corner[0]-px), num6(corner[1]-py)
		args["x"+strconv.Itoa(i)] = fmt.Sprintf("%s+%s*%s*cos(%s)-%s*%s*sin(%s)", cx, sx, dx, r, sy, dy, r)
		args["y"+strconv.Itoa(i)] = fmt.Sprintf("%s+%s*%s*sin(%s)+%s*%s*cos(%s)", cy, sx, dx, r, sy, dy, r)
	}
	return args
}

// textClipPanel draws a text clip's glass panel while the clip shows; its text is in the run's ASS.
func textClipPanel(base *ffmpeg.Stream, clip ClipPlan, canvas bool) *ffmpeg.Stream {
	enable := clipEnable(clip)
	for _, fx := range clip.Layer.Effects {
		if fx.Type != "glass_panel" {
			continue
		}
		color := fx.Color
		if color == "" {
			color = "#FFFFFF"
		}
		layer := clip.Layer
		x, y := layer.X-int(fx.PaddingX), layer.Y-int(fx.PaddingY)
		w, h := layer.Width+2*int(fx.PaddingX), layer.Height+2*int(fx.PaddingY)
		if fx.Blur > 0 {
			base = blurPanel(base, x, y, w, h, fx.Blur, enable, canvas)
		}
		base = base.Filter("drawbox", nil, canvasBox(ffmpeg.KwArgs{"x": x, "y": y, "w": w, "h": h, "color": withOpacity(color, fx.Opacity), "t": "fill", "enable": enable}, canvas))
	}
	return base
}

func clipWindow(clip ClipPlan) string {
	return fmt.Sprintf("between(t,%s,%s)", ffTime(clip.Start), ffTime(clip.Start+clip.Duration))
}

// applyEffectClip runs an effect clip's stacked adjustments over everything
// composited so far, limited to the clip's window and blended by its mix. On a
// folder canvas the adjustments keep the canvas's alpha.
//
// Adjustments that work in RGB convert every frame they receive, even frames
// their enable expression leaves untouched. On the frame, only the frames of
// such a clip's window go through it, laid back over the stream. The window
// may reach a frame past the clip's ends; enable still limits the effect to
// the clip.
func applyEffectClip(base *ffmpeg.Stream, s space, clip ClipPlan, canvas bool) *ffmpeg.Stream {
	if canvas || !slices.ContainsFunc(clip.Adjustments, AdjustmentPlan.rgb) {
		return effectClip(base, s, clip, canvas, perspectiveTime(s.fps, s.first))
	}
	// Frame numbers, not times: trim rounds times to the stream's time base.
	fps := float64(s.fps)
	first := max(int(math.Round(clip.Start.Seconds()*fps)), s.first)
	last := int(math.Round((clip.Start + clip.Duration).Seconds() * fps))
	split := base.Split()
	window := split.Get("1").Filter("trim", nil, ffmpeg.KwArgs{"start_frame": first - s.first, "end_frame": last + 1 - s.first})
	// perspective counts the window's frames; its first frame is frame first.
	return split.Get("0").Overlay(effectClip(window, s, clip, false, perspectiveTime(s.fps, first)), eofActionPass, ffmpeg.KwArgs{"x": 0, "y": 0})
}

// effectClip is applyEffectClip on a stream whose frames perspective reaches
// at streamTime, the time of each frame on the clip's owner timeline.
func effectClip(base *ffmpeg.Stream, s space, clip ClipPlan, canvas bool, streamTime string) *ffmpeg.Stream {
	enable := clipEnable(clip)
	anim := clip.Layer.Animation
	mixed := anim.Has("opacity") || clip.Mix < 1
	format := "yuv420p"
	if canvas {
		format = "yuva444p"
	}

	original, effected := base, base
	if mixed {
		split := base.Filter("format", ffmpeg.Args{format}).Split()
		original, effected = split.Get("0"), split.Get("1")
	}

	w, h := float64(s.W), float64(s.H)
	if anim.Has(cameraProps...) {
		t := fmt.Sprintf("(%s-%s)", streamTime, ffTime(clip.Start))
		args := transformCorners(anim, t, w, h, w/2, h/2)
		args["enable"] = enable
		effected = effected.Filter("perspective", nil, args)
	}
	for _, a := range clip.Adjustments {
		if a.Type == "vignette" && canvas {
			// vignette has no alpha formats: grade the color and restore the alpha.
			split := effected.Split()
			alpha := split.Get("1").Filter("alphaextract", nil)
			effected = ffmpeg.Filter([]*ffmpeg.Stream{adjust(split.Get("0"), a, enable, streamTime, w, h), alpha}, "alphamerge", nil)
			continue
		}
		effected = adjust(effected, a, enable, streamTime, w, h)
	}

	if !mixed {
		return effected
	}
	m := fmt.Sprintf("clip(%s*%s,0,1)", num6(clip.Mix), anim.Expr("opacity", fmt.Sprintf("(T-%s)", ffTime(clip.Start)), 1))
	// maskedmerge computes A+(B-A)*m with the mix as a per-frame mask.
	split := original.Split()
	planes := []string{"lum"}
	if canvas {
		planes = append(planes, "a")
	}
	mask := frameMask(split.Get("1"), m, s.W, s.H, planes...)
	return ffmpeg.Filter([]*ffmpeg.Stream{split.Get("0"), effected.Filter("format", ffmpeg.Args{format}), mask}, "maskedmerge", nil, ffmpeg.KwArgs{
		"enable": enable,
	})
}

// rgb reports whether an adjustment's filters work in RGB only.
func (a AdjustmentPlan) rgb() bool {
	switch a.Type {
	case "glow", "rgb_split":
		return true
	case "color_filter":
		return a.Preset == "sepia" || a.Preset == "warm" || a.Preset == "cool"
	}
	return false
}

func adjust(s *ffmpeg.Stream, a AdjustmentPlan, enable, t string, w, h float64) *ffmpeg.Stream {
	switch a.Type {
	case "blur":
		return s.Filter("gblur", nil, ffmpeg.KwArgs{"sigma": a.Radius, "enable": enable})
	case "color":
		s = s.Filter("eq", nil, ffmpeg.KwArgs{"brightness": a.Brightness, "contrast": a.Contrast, "saturation": a.Saturation, "gamma": a.Gamma, "enable": enable})
		if a.Hue != 0 {
			s = s.Filter("hue", nil, ffmpeg.KwArgs{"h": a.Hue, "enable": enable})
		}
		return s
	case "color_filter":
		v := a.Intensity
		switch a.Preset {
		case "grayscale":
			return s.Filter("hue", nil, ffmpeg.KwArgs{"s": 1 - v, "enable": enable})
		case "sepia":
			return s.Filter("colorchannelmixer", nil, ffmpeg.KwArgs{"rr": 1 - .607*v, "rg": .769 * v, "rb": .189 * v, "gr": .349 * v, "gg": 1 - .314*v, "gb": .168 * v, "br": .272 * v, "bg": .534 * v, "bb": 1 - .869*v, "enable": enable})
		case "warm":
			return s.Filter("colorbalance", nil, ffmpeg.KwArgs{"rs": .15 * v, "bs": -.15 * v, "enable": enable})
		case "cool":
			return s.Filter("colorbalance", nil, ffmpeg.KwArgs{"rs": -.15 * v, "bs": .15 * v, "enable": enable})
		default:
			return s.Filter("eq", nil, ffmpeg.KwArgs{"contrast": 1 + v*.6, "enable": enable})
		}
	case "vignette":
		// Opacity 0 leaves the frame untouched and 1 is a heavy vignette (lens angle 0.9 rad).
		return s.Filter("vignette", nil, ffmpeg.KwArgs{"angle": num6(math.Max(a.Opacity*0.9, 0.001)), "enable": enable})
	case "glow":
		// Screen-blend in RGB: in YUV, screening the neutral chroma planes tints the frame.
		split := s.Filter("format", ffmpeg.Args{"gbrap"}).Split()
		halo := split.Get("1").Filter("gblur", nil, ffmpeg.KwArgs{"sigma": a.Radius, "enable": enable})
		return ffmpeg.Filter([]*ffmpeg.Stream{split.Get("0"), halo}, "blend", nil, ffmpeg.KwArgs{"all_mode": "screen", "all_opacity": a.Strength, "enable": enable})
	case "rgb_split":
		shift := int(math.Round(a.Amount))
		return s.Filter("rgbashift", nil, ffmpeg.KwArgs{"rh": -shift, "bh": shift, "enable": enable})
	case "grain":
		// Color components only: on a folder canvas, alpha noise would speckle the edges.
		amount := int(math.Round(a.Amount))
		return s.Filter("noise", nil, ffmpeg.KwArgs{"c0s": amount, "c1s": amount, "c2s": amount, "allf": "t+u", "enable": enable})
	case "tint":
		return s.Filter("drawbox", nil, ffmpeg.KwArgs{"x": 0, "y": 0, "w": "iw", "h": "ih", "color": withOpacity(a.Color, a.Opacity), "t": "fill", "enable": enable})
	case "shake":
		// Translate the frame on two incommensurate sines, zoomed just enough to hide the edges.
		zoom := 1 + (2*a.Amount+2)/math.Min(w, h)
		dx := fmt.Sprintf("%s*sin(2*PI*%s*%s)", num6(a.Amount), num6(a.Frequency), t)
		dy := fmt.Sprintf("%s*cos(2*PI*%s*%s*1.37)", num6(a.Amount), num6(a.Frequency), t)
		args := ffmpeg.KwArgs{"sense": "destination", "eval": "frame", "enable": enable}
		for i, c := range [][2]float64{{0, 0}, {w, 0}, {0, h}, {w, h}} {
			args["x"+strconv.Itoa(i)] = fmt.Sprintf("%s+%s", num6(w/2+(c[0]-w/2)*zoom), dx)
			args["y"+strconv.Itoa(i)] = fmt.Sprintf("%s+%s", num6(h/2+(c[1]-h/2)*zoom), dy)
		}
		return s.Filter("perspective", nil, args)
	}
	return s
}
