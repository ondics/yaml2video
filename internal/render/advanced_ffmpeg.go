package render

import (
	"fmt"
	"math"
	"path/filepath"
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
type space struct {
	Rect
	fps int
}

func frameSpace(video VideoSpec) space {
	return space{Rect{0, 0, video.Width, video.Height}, video.FPS}
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
				for ; i < len(track.Clips) && track.Clips[i].Kind == "text"; i++ {
					text := track.Clips[i]
					text.Layer = s.local(text.Layer)
					base = textClipPanel(base, text, canvas)
				}
				i--
				args := plan.assArgs(filepath.Join(plan.WorkDir, "ass", runASSName(track, run)))
				if canvas {
					args["alpha"] = 1
				}
				base = base.Filter(ffmpegFilterASS, nil, args)
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
	inner := space{*clip.Folder.Canvas, s.fps}
	// The format must be part of the lavfi source: as a separate input, the
	// transparent color would be negotiated to a format without alpha first.
	source := fmt.Sprintf("color=c=black@0:s=%dx%d:r=%d:d=%s,format=yuva444p", inner.W, inner.H, s.fps, ffTime(length))
	group := ffmpeg.Input(source, ffmpeg.KwArgs{"f": "lavfi"})
	group = composite(group, plan, []TrackPlan{*clip.Folder}, inner, true, ordinal)

	layer := clip.Layer
	if layer.Opacity != 1 {
		group = group.ColorChannelMixer(ffmpeg.KwArgs{"aa": layer.Opacity})
	}
	group, x, y := animateMedia(group, s, layer, clip.Start)
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
	var input *ffmpeg.Stream
	if clip.Kind == "image" {
		input = mediaLayerStream(plan, ScenePlan{Duration: length}, layer)
	} else {
		input = shapeStream(plan, layer, length)
	}

	input, x, y := animateMedia(input, s, layer, clip.Start)
	if clip.Start > 0 {
		input = input.Filter("setpts", ffmpeg.Args{fmt.Sprintf("PTS+%s/TB", ffTime(clip.Start))})
	}
	args := ffmpeg.KwArgs{"x": x, "y": y}
	if enable := layer.enableExpr(); enable != "" {
		args["enable"] = enable
	}
	return base.Overlay(input, eofActionPass, overlayArgs(args, canvas))
}

func shapeStream(plan *Plan, layer LayerPlan, d time.Duration) *ffmpeg.Stream {
	source := fmt.Sprintf("color=c=%s:s=%dx%d:r=%d:d=%s", layer.Color, layer.Width, layer.Height, plan.Video.FPS, ffTime(d))
	input := ffmpeg.Input(source, ffmpeg.KwArgs{"f": "lavfi"}).Filter("format", ffmpeg.Args{"rgba"})
	if layer.Opacity != 1 {
		input = input.ColorChannelMixer(ffmpeg.KwArgs{"aa": layer.Opacity})
	}
	if masked(layer) {
		input = maskAlpha(input, shapeMask(layer))
	}
	return input
}

// animateMedia applies keyframes to a prepared media stream whose own timeline
// starts at the clip start. It returns the stream and its overlay position in
// s (the layer is in s's coordinates); start converts the main stream's time
// t into clip time for x/y expressions.
func animateMedia(input *ffmpeg.Stream, s space, layer LayerPlan, start time.Duration) (*ffmpeg.Stream, any, any) {
	anim := layer.Animation
	if anim == nil {
		return input, layer.X, layer.Y
	}
	// Keyframed opacity, and scales that reach 0 (perspective keeps a
	// one-pixel sliver of a layer scaled to nothing), become an alpha factor.
	if collapses := anim.reaches0("scale", "scale_x", "scale_y"); anim.Has("opacity") || collapses {
		alpha := fmt.Sprintf("alpha(X,Y)*clip(%s,0,1)", anim.Expr("opacity", "T", 1))
		if collapses {
			s := anim.Expr("scale", "T", 1)
			alpha += fmt.Sprintf("*gt(%s*%s,0.002)*gt(%s*%s,0.002)", s, anim.Expr("scale_x", "T", 1), s, anim.Expr("scale_y", "T", 1))
		}
		input = input.Filter("format", ffmpeg.Args{"gbrap"}).Filter("geq", nil, ffmpeg.KwArgs{
			"r": "r(X,Y)", "g": "g(X,Y)", "b": "b(X,Y)", "a": alpha,
		})
	}
	if !anim.Has("scale", "scale_x", "scale_y", "rotation") {
		if !anim.Has("x", "y") {
			return input, layer.X, layer.Y
		}
		t := fmt.Sprintf("(t-%s)", ffTime(start))
		return input, fmt.Sprintf("%d+%s", layer.X, anim.Expr("x", t, 0)), fmt.Sprintf("%d+%s", layer.Y, anim.Expr("y", t, 0))
	}

	// Scale and rotation: place the layer on a transparent canvas that covers
	// the space and the layer, then move the canvas corners with perspective.
	w, h := s.W, s.H
	left := min(0, layer.X) - animationMargin
	top := min(0, layer.Y) - animationMargin
	cw := max(w, layer.X+layer.Width) + animationMargin - left
	ch := max(h, layer.Y+layer.Height) + animationMargin - top
	ox, oy := layer.X-left, layer.Y-top
	px, py := float64(ox)+float64(layer.Width)/2, float64(oy)+float64(layer.Height)/2
	if layer.Pivot != nil {
		px, py = layer.Pivot[0]-float64(left), layer.Pivot[1]-float64(top)
	}

	t := fmt.Sprintf("(in/%d)", s.fps)
	input = input.Filter("format", ffmpeg.Args{"yuva444p"}).
		Filter("pad", nil, ffmpeg.KwArgs{"w": cw, "h": ch, "x": ox, "y": oy, "color": "black@0"}).
		Filter("perspective", nil, transformCorners(anim, t, float64(cw), float64(ch), px, py))
	return input, left, top
}

// transformCorners maps the corners of a cw×ch canvas through translate, scale,
// and clockwise rotation around (px, py), as perspective destination points.
func transformCorners(anim *Animation, t string, cw, ch, px, py float64) ffmpeg.KwArgs {
	sx := fmt.Sprintf("max(%s*%s,0.001)", anim.Expr("scale", t, 1), anim.Expr("scale_x", t, 1))
	sy := fmt.Sprintf("max(%s*%s,0.001)", anim.Expr("scale", t, 1), anim.Expr("scale_y", t, 1))
	r := fmt.Sprintf("(%s*PI/180)", anim.Expr("rotation", t, 0))
	cx := fmt.Sprintf("(%s+%s)", num6(px), anim.Expr("x", t, 0))
	cy := fmt.Sprintf("(%s+%s)", num6(py), anim.Expr("y", t, 0))

	args := ffmpeg.KwArgs{"sense": "destination", "eval": "frame", "interpolation": "linear"}
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
			split := base.Split()
			blurred := split.Get("1").Filter("gblur", nil, ffmpeg.KwArgs{"sigma": fx.Blur})
			mask := fmt.Sprintf("between(X,%d,%d)*between(Y,%d,%d)", x, x+w-1, y, y+h-1)
			base = split.Get("0").Overlay(maskAlpha(blurred, mask), eofActionEndAll, overlayArgs(ffmpeg.KwArgs{"x": 0, "y": 0, "enable": enable}, canvas))
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
func applyEffectClip(base *ffmpeg.Stream, s space, clip ClipPlan, canvas bool) *ffmpeg.Stream {
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

	fps := s.fps
	w, h := float64(s.W), float64(s.H)
	if anim.Has(cameraProps...) {
		t := fmt.Sprintf("(in/%d-%s)", fps, ffTime(clip.Start))
		args := transformCorners(anim, t, w, h, w/2, h/2)
		args["enable"] = enable
		effected = effected.Filter("perspective", nil, args)
	}
	for _, a := range clip.Adjustments {
		if a.Type == "vignette" && canvas {
			// vignette has no alpha formats: grade the color and restore the alpha.
			split := effected.Split()
			alpha := split.Get("1").Filter("alphaextract", nil)
			effected = ffmpeg.Filter([]*ffmpeg.Stream{adjust(split.Get("0"), a, enable, fps, w, h), alpha}, "alphamerge", nil)
			continue
		}
		effected = adjust(effected, a, enable, fps, w, h)
	}

	if !mixed {
		return effected
	}
	m := fmt.Sprintf("%s*%s", num6(clip.Mix), anim.Expr("opacity", fmt.Sprintf("(T-%s)", ffTime(clip.Start)), 1))
	return ffmpeg.Filter([]*ffmpeg.Stream{original, effected.Filter("format", ffmpeg.Args{format})}, "blend", nil, ffmpeg.KwArgs{
		"all_expr": fmt.Sprintf("A+(B-A)*clip(%s,0,1)", m),
		"enable":   enable,
	})
}

func adjust(s *ffmpeg.Stream, a AdjustmentPlan, enable string, fps int, w, h float64) *ffmpeg.Stream {
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
		t := fmt.Sprintf("(in/%d)", fps)
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
