package v2

import (
	"fmt"
	"math"
	"strings"

	"github.com/ondics/yaml2video/render"
)

func bounds(el obj, video render.VideoSpec, w, h int) (int, int) {
	place := object(el["placement"])
	parts := strings.Split(str(place["anchor"]), "-")
	anchor := str(place["anchor"])
	x, y := 0, 0
	if anchor == "center" {
		x = (video.Width - w) / 2
		y = (video.Height - h) / 2
	} else {
		switch parts[len(parts)-1] {
		case "center":
			x = (video.Width - w) / 2
		case "right":
			x = video.Width - w
		}
		switch parts[0] {
		case "center":
			y = (video.Height - h) / 2
		case "bottom":
			y = video.Height - h
		}
	}
	return x + int(math.Round(num(place["offset_x"]))), y + int(math.Round(num(place["offset_y"])))
}

func effects(el obj, kind string) ([]render.EffectPlan, error) {
	var result []render.EffectPlan
	arr, _ := el["effects"].([]any)
	for _, raw := range arr {
		v := object(raw)
		typ := str(v["type"])
		text := typ == "text_shadow" || typ == "text_glow" || typ == "text_outline" || typ == "glass_panel"
		image := typ == "pan_zoom" || typ == "color_filter" || typ == "vignette" || typ == "blur"
		if text && kind != "text" || image && kind != "image" {
			return nil, fmt.Errorf("effect %s is not applicable to %s", typ, kind)
		}
		if text && typ != "glass_panel" && v["color"] != nil && !assColorSupported(str(v["color"])) {
			return nil, fmt.Errorf("effect %s color %q unsupported by ASS renderer", typ, v["color"])
		}

		if typ == "vignette" && str(v["color"]) != "" && str(v["color"]) != "#000000" && str(v["color"]) != "black" {
			return nil, fmt.Errorf("colored vignette is unsupported by render.Plan")
		}
		fx := render.EffectPlan{Type: typ, Color: str(v["color"]), Opacity: num(choose(v["opacity"], jsonNumber(1))), OffsetX: num(v["offset_x"]), OffsetY: num(v["offset_y"]), Blur: num(v["blur"]), Width: num(v["width"]), FromScale: num(choose(v["from_scale"], jsonNumber(1))), ToScale: num(choose(v["to_scale"], jsonNumber(1))), FromAnchor: str(choose(v["from_anchor"], "center")), ToAnchor: str(choose(v["to_anchor"], "center")), Preset: str(v["preset"]), Intensity: num(choose(v["intensity"], jsonNumber(1))), Radius: num(v["radius"])}
		if padding, ok := v["padding"].([]any); ok {
			fx.PaddingX = num(padding[0])
			fx.PaddingY = num(padding[1])
		} else {
			fx.PaddingX = num(v["padding"])
			fx.PaddingY = fx.PaddingX
		}
		var e error
		fx.FadeIn, e = duration(v["fade_in"])
		if e != nil {
			return nil, e
		}
		fx.FadeOut, e = duration(v["fade_out"])
		if e != nil {
			return nil, e
		}
		result = append(result, fx)
	}
	return result, nil
}

func mediaLayer(source string, el, defaults obj, video render.VideoSpec) (render.LayerPlan, error) {
	w := int(math.Round(float64(video.Width) * ratio(el["max_width_ratio"])))
	h := int(math.Round(float64(video.Height) * ratio(el["max_height_ratio"])))
	if w < 1 || h < 1 {
		return render.LayerPlan{}, fmt.Errorf("media bounds empty")
	}

	x, y := bounds(el, video, w, h)
	fx, e := effects(el, "image")
	if e != nil {
		return render.LayerPlan{}, e
	}
	shape := str(el["shape"])

	if shape == "" {
		shape = "rectangle"
	}
	return render.LayerPlan{Kind: "image", Path: source, Fit: str(choose(el["fit"], defaults["default_fit"], "cover")), Anchor: str(object(el["placement"])["anchor"]), Shape: shape, X: x, Y: y, Width: w, Height: h, Opacity: 1, Effects: fx}, nil
}

func textLayer(value, key string, el, typography, theme obj, video render.VideoSpec) (render.LayerPlan, error) {
	style := object(el["style"])
	size := integer(choose(style["font_size"], typography["body_size"], jsonNumber(32)))
	if key == "title" {
		size = integer(choose(style["font_size"], typography["title_size"], typography["body_size"], jsonNumber(48)))
	}
	if key == "tip" {
		size = integer(choose(style["font_size"], typography["tip_size"], typography["body_size"], jsonNumber(28)))
	}
	if size <= 0 {
		return render.LayerPlan{}, fmt.Errorf("no font size configured")
	}
	if spacing := num(style["line_spacing"]); spacing != 0 {
		return render.LayerPlan{}, fmt.Errorf("line_spacing requires render.Plan support")
	}

	width := int(float64(video.Width) * ratio(el["max_width_ratio"]))
	if width < 1 {
		return render.LayerPlan{}, fmt.Errorf("text width empty")
	}

	maxChars := max(1, int(float64(width)/(float64(size)*0.4)))
	words := strings.Fields(value)
	var lines []string
	line := ""
	for _, word := range words {
		if line != "" && len([]rune(line))+1+len([]rune(word)) > maxChars {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	if maxLines := integer(el["max_lines"]); maxLines > 0 && len(lines) > maxLines {
		return render.LayerPlan{}, fmt.Errorf("text exceeds max_lines %d", maxLines)
	}

	height := int(math.Ceil(float64(len(lines)*size) * 1.2))
	x, y := bounds(el, video, width, height)
	fx, err := effects(el, "text")
	if err != nil {
		return render.LayerPlan{}, err
	}

	color := str(choose(style["color"], theme["text_color"]))
	if !assColorSupported(color) {
		return render.LayerPlan{}, fmt.Errorf("text color %q is unsupported by ASS renderer", color)
	}

	font := str(choose(style["font_family"], typography["font_family"], "Arial"))
	weight := str(style["weight"])
	if weight == "medium" {
		return render.LayerPlan{}, fmt.Errorf("medium font weight requires render.Plan support")
	}

	return render.LayerPlan{Kind: "text", X: x, Y: y, Width: width, Height: height, WrapWidth: width, Font: font, FontSize: size, Color: color, Opacity: 1, Spans: []render.TextSpan{{Content: strings.Join(lines, "\n"), Color: color, Bold: weight == "bold"}}, Effects: fx}, nil
}

func ratio(v any) float64 {
	if v == nil {
		return 1
	}
	return num(v)
}

func addProgress(scene *render.ScenePlan, el obj, current, total int, theme obj, video render.VideoSpec) error {
	if el["visible"] == false {
		return nil
	}
	w := int(float64(video.Width) * ratio(choose(el["width_ratio"], jsonNumber(0.8))))
	h := int(math.Round(num(choose(el["thickness"], jsonNumber(8)))))
	if w < 1 || h < 1 {
		return fmt.Errorf("progress dimensions empty")
	}
	x, y := bounds(el, video, w, h)
	background := str(choose(el["background_color"], theme["muted_color"], theme["background"]))
	color := str(choose(el["color"], theme["accent_color"]))
	scene.Layers = append(scene.Layers, render.LayerPlan{Kind: "rectangle", X: x, Y: y, Width: w, Height: h, Color: background, Opacity: 1}, render.LayerPlan{Kind: "rectangle", X: x, Y: y, Width: max(1, int(math.Round(float64(w)*float64(current)/float64(total)))), Height: h, Color: color, Opacity: 1})
	return nil
}

func jsonNumber(n float64) any { return n }
