package render

import (
	"fmt"
	"math"
	"strings"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

func validateEffects(layer LayerPlan, duration time.Duration) error {
	switch layer.Shape {
	case "", "rectangle":
	case "rounded-rectangle", "circle":
		if layer.Kind != "image" && layer.Kind != "video" {
			return fmt.Errorf("media shape %q requires an image or video layer", layer.Shape)
		}
		if layer.Width <= 0 || layer.Height <= 0 {
			return fmt.Errorf("media shape %q requires positive width and height", layer.Shape)
		}
	default:
		return fmt.Errorf("media shape %q is unsupported", layer.Shape)
	}

	if layer.CornerRadius < 0 || (layer.CornerRadius > 0 && layer.Shape != "rounded-rectangle") {
		return fmt.Errorf("corner radius must be non-negative and requires rounded-rectangle shape")
	}

	glassPanels := 0
	for i, e := range layer.Effects {
		fail := func(reason string) error { return fmt.Errorf("effect %d (%s): %s", i, e.Type, reason) }
		media := layer.Kind == "image" || layer.Kind == "video"
		text := layer.Kind == "text"

		switch e.Type {
		case "pan_zoom":
			if !media {
				return fail("requires an image or video layer")
			}
			if e.FromScale < 1 || e.ToScale < 1 {
				return fail("scales must be at least 1")
			}
			if _, err := anchorXY(e.FromAnchor); err != nil {
				return fail(err.Error())
			}
			if _, err := anchorXY(e.ToAnchor); err != nil {
				return fail(err.Error())
			}
		case "color_filter":
			if !media {
				return fail("requires an image or video layer")
			}
			switch e.Preset {
			case "grayscale", "sepia", "warm", "cool", "high-contrast":
			default:
				return fail("unsupported color preset " + e.Preset)
			}
			if e.Intensity < 0 || e.Intensity > 1 {
				return fail("intensity must be between 0 and 1")
			}
		case "vignette", "blur":
			if e.Type == "vignette" && e.Color != "" && e.Color != "#000000" && e.Color != "0x000000" && e.Color != "black" {
				return fail("colored vignette is unsupported; only black is available")
			}
			if !media {
				return fail("requires an image or video layer")
			}
			if e.Radius < 0 {
				return fail("radius must not be negative")
			}
		case "text_shadow", "text_glow", "text_outline", "glass_panel":
			if !text {
				return fail("requires a text layer")
			}
			if e.Blur < 0 || e.Width < 0 || e.PaddingX < 0 || e.PaddingY < 0 {
				return fail("sizes must not be negative")
			}
			if e.Type == "glass_panel" {
				glassPanels++
				if glassPanels > 1 {
					return fail("multiple glass panels on one layer are unsupported")
				}
				if layer.Width <= 0 || layer.Height <= 0 {
					return fail("glass panel requires positive width and height")
				}
			}

		case "fade":
			if !media && !text {
				return fail("requires a media or text layer")
			}
			if e.FadeIn < 0 || e.FadeOut < 0 || e.FadeIn > duration || e.FadeOut > duration {
				return fail("fade durations must fit within the scene")
			}
		default:
			return fail("unsupported effect")
		}

		if e.Opacity < 0 || e.Opacity > 1 {
			return fail("opacity must be between 0 and 1")
		}
		if e.Type == "glass_panel" && layer.Background != nil {
			return fail("glass_panel with an existing text background is unsupported")
		}
		if e.Type == "glass_panel" && len(layer.ItemBackgrounds) > 0 {
			return fail("glass_panel with item backgrounds is unsupported")
		}
		if e.Type == "fade" && text && (layer.Background != nil || len(layer.ItemBackgrounds) > 0 || hasEffect(layer.Effects, "glass_panel")) {
			return fail("fading text backgrounds is unsupported")
		}
	}
	return nil
}

func hasEffect(effects []EffectPlan, kind string) bool {
	for _, e := range effects {
		if e.Type == kind {
			return true
		}
	}
	return false
}

func anchorXY(anchor string) ([2]float64, error) {
	switch anchor {
	case "", "center":
		return [2]float64{.5, .5}, nil
	case "top-left":
		return [2]float64{0, 0}, nil
	case "top-center":
		return [2]float64{.5, 0}, nil
	case "top-right":
		return [2]float64{1, 0}, nil
	case "center-left":
		return [2]float64{0, .5}, nil
	case "center-right":
		return [2]float64{1, .5}, nil
	case "bottom-left":
		return [2]float64{0, 1}, nil
	case "bottom-center":
		return [2]float64{.5, 1}, nil
	case "bottom-right":
		return [2]float64{1, 1}, nil
	default:
		return [2]float64{}, fmt.Errorf("unsupported anchor %q", anchor)
	}
}

func applyMediaEffects(input *ffmpeg.Stream, plan *Plan, scene ScenePlan, layer LayerPlan) *ffmpeg.Stream {
	for _, e := range layer.Effects {
		switch e.Type {
		case "pan_zoom":
			from, _ := anchorXY(e.FromAnchor)
			to, _ := anchorXY(e.ToAnchor)
			frames := math.Max(1, scene.Duration.Seconds()*float64(plan.Video.FPS)-1)
			progress := fmt.Sprintf("min(on/%g,1)", frames)
			zoom := fmt.Sprintf("(%g+(%g-%g)*%s)", e.FromScale, e.ToScale, e.FromScale, progress)
			x := fmt.Sprintf("(iw-iw/zoom)*(%g+(%g-%g)*%s)", from[0], to[0], from[0], progress)
			y := fmt.Sprintf("(ih-ih/zoom)*(%g+(%g-%g)*%s)", from[1], to[1], from[1], progress)
			input = input.Filter("zoompan", nil, ffmpeg.KwArgs{"z": zoom, "x": x, "y": y, "d": 1, "s": fmt.Sprintf("%dx%d", layer.Width, layer.Height), "fps": plan.Video.FPS})
		case "blur":
			if e.Radius > 0 {
				input = input.Filter("gblur", nil, ffmpeg.KwArgs{"sigma": e.Radius})
			}
		case "color_filter":
			v := e.Intensity
			switch e.Preset {
			case "grayscale":
				input = input.Filter("hue", nil, ffmpeg.KwArgs{"s": 1 - v})
			case "sepia":
				input = input.Filter("colorchannelmixer", nil, ffmpeg.KwArgs{"rr": 1 - .607*v, "rg": .769 * v, "rb": .189 * v, "gr": .349 * v, "gg": 1 - .314*v, "gb": .168 * v, "br": .272 * v, "bg": .534 * v, "bb": 1 - .869*v})
			case "warm":
				input = input.Filter("colorbalance", nil, ffmpeg.KwArgs{"rs": .15 * v, "bs": -.15 * v})
			case "cool":
				input = input.Filter("colorbalance", nil, ffmpeg.KwArgs{"rs": -.15 * v, "bs": .15 * v})
			case "high-contrast":
				input = input.Filter("eq", nil, ffmpeg.KwArgs{"contrast": 1 + v*.6})
			}
		case "vignette":
			// vignette darkens pixels but cannot tint them; non-black tint is rejected in validation.
			input = input.Filter("vignette", nil, ffmpeg.KwArgs{"angle": fmt.Sprintf("PI/2-%g*(PI/2-PI/5)", e.Opacity), "eval": "frame"})
		case "fade":
			input = input.Filter("format", ffmpeg.Args{"rgba"})
			if e.FadeIn > 0 {
				input = input.Filter("fade", nil, ffmpeg.KwArgs{"t": "in", "st": 0, "d": ffTime(e.FadeIn), "alpha": 1})
			}
			if e.FadeOut > 0 {
				input = input.Filter("fade", nil, ffmpeg.KwArgs{"t": "out", "st": ffTime(scene.Duration - e.FadeOut), "d": ffTime(e.FadeOut), "alpha": 1})
			}
		}
	}
	return input
}

func textEffectTags(effects []EffectPlan) (string, int, int) {
	var tags strings.Builder
	var fadeIn, fadeOut int
	for _, e := range effects {
		color := e.Color
		if after, ok := strings.CutPrefix(color, "#"); ok {
			color = "0x" + after
		}
		if color == "" {
			color = "0x000000"
		}

		switch e.Type {
		case "text_shadow":
			fmt.Fprintf(&tags, "\\4c%s\\4a%s\\xshad%g\\yshad%g\\blur%g", assColor(color), assAlpha(color, e.Opacity), e.OffsetX, e.OffsetY, e.Blur)
		case "text_outline", "text_glow":
			width := e.Width
			if e.Type == "text_glow" {
				width = math.Max(1, e.Blur*2)
			}
			fmt.Fprintf(&tags, "\\3c%s\\3a%s\\bord%g\\blur%g", assColor(color), assAlpha(color, e.Opacity), width, e.Blur)
		case "fade":
			fadeIn = int(e.FadeIn.Milliseconds())
			fadeOut = int(e.FadeOut.Milliseconds())
		}
	}
	return tags.String(), fadeIn, fadeOut
}
