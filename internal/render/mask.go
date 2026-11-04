package render

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// maskAlpha retains the incoming transparency (including fades and transparent
// padding) while clipping a media layer to its requested geometry. geq runs
// its expression for every pixel of every frame, by far the costliest step of
// a render, so it only computes the coverage on the alpha plane, and a static
// mask (one without T) only once: blend repeats that frame for the rest.
func maskAlpha(input *ffmpeg.Stream, mask string, static bool) *ffmpeg.Stream {
	split := input.Filter("format", ffmpeg.Args{"gbrap"}).Split()
	alpha := split.Get("1").Filter("alphaextract", nil)
	if static {
		coverage := alpha.Split()
		mask := coverage.Get("1").
			Filter("trim", nil, ffmpeg.KwArgs{"end_frame": 1}).
			Filter("geq", nil, ffmpeg.KwArgs{"lum": fmt.Sprintf("255*(%s)", mask)})
		alpha = ffmpeg.Filter([]*ffmpeg.Stream{coverage.Get("0"), mask}, "blend", nil, ffmpeg.KwArgs{"all_mode": "multiply"})
	} else {
		alpha = alpha.Filter("geq", nil, ffmpeg.KwArgs{"lum": fmt.Sprintf("lum(X,Y)*(%s)", mask)})
	}
	return ffmpeg.Filter([]*ffmpeg.Stream{split.Get("0"), alpha}, "alphamerge", nil)
}

// frameMask is a w×h stream, in src's format and following its frames, whose
// planes hold 255×expr, a value of the frame time T in 0–1. It evaluates expr
// once per frame, on a 2×2 crop of src that a nearest-neighbor scale spreads:
// evaluating a per-frame value for every pixel (with geq or a blend
// expression) costs far more. Planes are geq's plane names; cb and cr follow
// lum unless given.
func frameMask(src *ffmpeg.Stream, expr string, w, h int, planes ...string) *ffmpeg.Stream {
	args := ffmpeg.KwArgs{}
	for _, plane := range planes {
		args[plane] = fmt.Sprintf("255*(%s)", expr)
	}
	return src.Filter("crop", nil, ffmpeg.KwArgs{"w": "min(2,iw)", "h": "min(2,ih)", "x": 0, "y": 0}).
		Filter("geq", nil, args).
		Filter("scale", nil, ffmpeg.KwArgs{"w": w, "h": h, "flags": "neighbor"})
}

// scaleAlpha multiplies the alpha of a w×h stream by factor, a value of the
// frame time T in 0–1; see frameMask.
func scaleAlpha(input *ffmpeg.Stream, factor string, w, h int) *ffmpeg.Stream {
	split := input.Filter("format", ffmpeg.Args{"gbrap"}).Split()
	alpha := split.Get("1").Filter("alphaextract", nil).Split()
	mask := frameMask(alpha.Get("1"), factor, w, h, "lum")
	scaled := ffmpeg.Filter([]*ffmpeg.Stream{alpha.Get("0"), mask}, "blend", nil, ffmpeg.KwArgs{"all_mode": "multiply"})
	return ffmpeg.Filter([]*ffmpeg.Stream{split.Get("0"), scaled}, "alphamerge", nil)
}

// maskShape clips a layer to its shape and animated size; see maskAlpha.
func maskShape(input *ffmpeg.Stream, layer LayerPlan) *ffmpeg.Stream {
	return maskAlpha(input, shapeMask(layer), !layer.Animation.Has("width", "height"))
}

// masked reports whether a layer needs an alpha mask for its shape or its
// animated size.
func masked(layer LayerPlan) bool {
	return layer.Shape == "circle" || layer.Shape == "rounded-rectangle" || layer.Animation.Has("width", "height")
}

func shapeMask(layer LayerPlan) string {
	if layer.Animation.Has("width", "height") {
		return sizeMask(layer)
	}
	w, h := float64(layer.Width), float64(layer.Height)
	switch layer.Shape {
	case "circle":
		radius := min(w, h) / 2
		return fmt.Sprintf("clip(%g+0.5-sqrt(pow(X+0.5-%g,2)+pow(Y+0.5-%g,2)),0,1)", radius, w/2, h/2)
	case "rounded-rectangle":
		radius := float64(layer.CornerRadius)
		if radius == 0 {
			radius = min(w, h) / 12
		}
		radius = min(radius, min(w, h)/2)
		return fmt.Sprintf("clip(%g+0.5-sqrt(pow(max(abs(X+0.5-%g)-%g,0),2)+pow(max(abs(Y+0.5-%g)-%g,0),2)),0,1)", radius, w/2, w/2-radius, h/2, h/2-radius)
	default:
		return "1"
	}
}

// sizeMask clips a layer laid out at its largest size (see fitAnimatedSize)
// to its width and height keyframes at time T, centered, with antialiased
// edges. Corner radii keep their size: a rounded rectangle keeps its radius
// (limited to half the shorter side) and a circle becomes a pill.
func sizeMask(layer LayerPlan) string {
	anim := layer.Animation
	hw := fmt.Sprintf("(%s/2)", anim.Expr("width", "T", float64(layer.Width)))
	hh := fmt.Sprintf("(%s/2)", anim.Expr("height", "T", float64(layer.Height)))
	r := "0"
	switch layer.Shape {
	case "circle":
		r = fmt.Sprintf("min(%s,%s)", hw, hh)
	case "rounded-rectangle":
		radius := fmt.Sprintf("min(%s,%s)/6", hw, hh)
		if layer.CornerRadius > 0 {
			radius = strconv.Itoa(layer.CornerRadius)
		}
		r = fmt.Sprintf("max(min(%s,min(%s,%s)),0)", radius, hw, hh)
	}
	// Signed distance from the pixel center to the rounded box, as coverage.
	// The keyframe chains are evaluated once per pixel into variables (0–4);
	// "0*st()" sequences them without ";", which separates filter chains.
	vars := fmt.Sprintf("0*st(0,%s)+0*st(1,%s)+0*st(2,%s)", hw, hh, strings.NewReplacer(hw, "ld(0)", hh, "ld(1)").Replace(r))
	vars += fmt.Sprintf("+0*st(3,abs(X+0.5-%s)-ld(0)+ld(2))+0*st(4,abs(Y+0.5-%s)-ld(1)+ld(2))",
		num6(float64(layer.Width)/2), num6(float64(layer.Height)/2))
	return vars + "+clip(0.5-hypot(max(ld(3),0),max(ld(4),0))-min(max(ld(3),ld(4)),0)+ld(2),0,1)"
}

// blurPanel blurs only the previously composited video, never the text or
// tint. Only the panel and a margin around it are blurred, so the panel sees
// the same neighbors as a full-frame blur; the crops clamp to the frame (whose
// size the expressions read as iw and ih) for panels near or past its edges.
func blurPanel(base *ffmpeg.Stream, x, y, width, height int, sigma float64, enable string, canvas bool) *ffmpeg.Stream {
	right, bottom := x+width, y+height
	if right <= 0 || bottom <= 0 {
		return base // entirely left of or above the frame
	}
	// overlay rounds positions on subsampled (yuv420p) frames down to even
	// pixels, so the patch runs from even offsets to even ends, and a mask
	// hides what it covers beyond the panel.
	patchX, patchY := max(x, 0)&^1, max(y, 0)&^1
	patchRight, patchBottom := (right+1)&^1, (bottom+1)&^1
	// The area crop runs on the frame's own format: even sizes for yuv420p.
	margin := (int(math.Ceil(3*sigma)) + 1) &^ 1
	areaX, areaY := max(patchX-margin, 0), max(patchY-margin, 0)
	blurArgs := ffmpeg.KwArgs{"sigma": sigma}
	if enable != "" {
		blurArgs["enable"] = enable
	}
	split := base.Split()
	patch := split.Get("1").
		Filter("crop", nil, ffmpeg.KwArgs{
			"x": areaX, "y": areaY,
			"w": fmt.Sprintf("max(min(%d,iw)-%d,2)", patchRight+margin, areaX),
			"h": fmt.Sprintf("max(min(%d,ih)-%d,2)", patchBottom+margin, areaY),
		}).
		Filter("format", ffmpeg.Args{"gbrap"}).
		Filter("gblur", nil, blurArgs).
		Filter("crop", nil, ffmpeg.KwArgs{
			"x": patchX - areaX, "y": patchY - areaY, "exact": 1,
			"w": fmt.Sprintf("max(min(%d,iw)-%d,1)", patchRight-areaX, patchX-areaX),
			"h": fmt.Sprintf("max(min(%d,ih)-%d,1)", patchBottom-areaY, patchY-areaY),
		})
	if patchX != x || patchY != y || patchRight != right || patchBottom != bottom {
		mask := fmt.Sprintf("between(X,%d,%d)*between(Y,%d,%d)", x-patchX, right-1-patchX, y-patchY, bottom-1-patchY)
		patch = maskAlpha(patch, mask, true)
	}
	args := ffmpeg.KwArgs{"x": patchX, "y": patchY}
	if enable != "" {
		args["enable"] = enable
	}
	return split.Get("0").Overlay(patch, eofActionPass, overlayArgs(args, canvas))
}
