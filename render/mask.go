package render

import (
	"fmt"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// maskAlpha retains the incoming transparency (including fades and transparent
// padding) while clipping a media layer to its requested geometry.
func maskAlpha(input *ffmpeg.Stream, mask string) *ffmpeg.Stream {
	return input.Filter("format", ffmpeg.Args{"gbrap"}).Filter("geq", nil, ffmpeg.KwArgs{
		"r": "r(X,Y)", "g": "g(X,Y)", "b": "b(X,Y)", "a": fmt.Sprintf("alpha(X,Y)*(%s)", mask),
	})
}

func shapeMask(layer LayerPlan) string {
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

// blurPanel blurs only the previously composited video, never the text or tint.
// The masked full-frame copy avoids out-of-bounds crops for panels near edges.
func blurPanel(base *ffmpeg.Stream, x, y, width, height int, sigma float64) *ffmpeg.Stream {
	split := base.Split()
	blurred := split.Get("1").Filter("gblur", nil, ffmpeg.KwArgs{"sigma": sigma})
	mask := fmt.Sprintf("between(X,%d,%d)*between(Y,%d,%d)", x, x+width-1, y, y+height-1)
	return split.Get("0").Overlay(maskAlpha(blurred, mask), eofActionEndAll, ffmpeg.KwArgs{"x": 0, "y": 0})
}
