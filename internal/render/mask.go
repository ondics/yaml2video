package render

import (
	"fmt"
	"strconv"
	"strings"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// maskAlpha retains the incoming transparency (including fades and transparent
// padding) while clipping a media layer to its requested geometry.
func maskAlpha(input *ffmpeg.Stream, mask string) *ffmpeg.Stream {
	return input.Filter("format", ffmpeg.Args{"gbrap"}).Filter("geq", nil, ffmpeg.KwArgs{
		"r": "r(X,Y)", "g": "g(X,Y)", "b": "b(X,Y)", "a": fmt.Sprintf("alpha(X,Y)*(%s)", mask),
	})
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

// blurPanel blurs only the previously composited video, never the text or tint.
// The masked full-frame copy avoids out-of-bounds crops for panels near edges.
func blurPanel(base *ffmpeg.Stream, x, y, width, height int, sigma float64) *ffmpeg.Stream {
	split := base.Split()
	blurred := split.Get("1").Filter("gblur", nil, ffmpeg.KwArgs{"sigma": sigma})
	mask := fmt.Sprintf("between(X,%d,%d)*between(Y,%d,%d)", x, x+width-1, y, y+height-1)
	return split.Get("0").Overlay(maskAlpha(blurred, mask), eofActionEndAll, ffmpeg.KwArgs{"x": 0, "y": 0})
}
