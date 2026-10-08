package v2

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ondics/yaml2video/render"
)

func assTime(d time.Duration) string {
	cs := int64(d / (10 * time.Millisecond))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}
func assAlpha(opacity float64) string     { return fmt.Sprintf("&H%02X&", int((1-opacity)*255+0.5)) }
func assColorSupported(color string) bool { return len(color) == 7 && strings.HasPrefix(color, "#") }
func assColor(color string) string {
	if strings.HasPrefix(color, "#") && len(color) == 7 {
		return "&H" + color[5:7] + color[3:5] + color[1:3] + "&"
	}
	return "&HFFFFFF&"
}

func escapeASS(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "{", "\\{")
	s = strings.ReplaceAll(s, "}", "\\}")
	return strings.ReplaceAll(s, "\n", "\\N")
}

func assDocument(video render.VideoSpec, scene render.ScenePlan, alignments map[int]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Script Info]\nScriptType: v4.00+\nPlayResX: %d\nPlayResY: %d\n\n[V4+ Styles]\nFormat: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\nStyle: Default,Arial,24,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,3,0,0,5,0,0,0,1\n\n[Events]\nFormat: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text\n", video.Width, video.Height)
	for i, layer := range scene.Layers {
		if layer.Kind != "text" {
			continue
		}
		bold := 0
		if len(layer.Spans) > 0 && layer.Spans[0].Bold {
			bold = 1
		}
		font := strings.NewReplacer("{", "", "}", "", "\\", "").Replace(layer.Font)
		value := ""
		for _, span := range layer.Spans {
			value += escapeASS(span.Content)
		}
		tags := ""
		fadeIn, fadeOut := 0, 0
		for _, fx := range layer.Effects {
			switch fx.Type {
			case "text_shadow":
				tags += fmt.Sprintf("\\4c%s\\4a%s\\xshad%g\\yshad%g\\blur%g", assColor(fx.Color), assAlpha(fx.Opacity), fx.OffsetX, fx.OffsetY, fx.Blur)
			case "text_outline", "text_glow":
				width := fx.Width
				if fx.Type == "text_glow" {
					width = math.Max(1, fx.Blur*2)
				}
				tags += fmt.Sprintf("\\3c%s\\3a%s\\bord%g\\blur%g", assColor(fx.Color), assAlpha(fx.Opacity), width, fx.Blur)
			case "fade":
				fadeIn, fadeOut = int(fx.FadeIn.Milliseconds()), int(fx.FadeOut.Milliseconds())
			}
		}
		if fadeIn > 0 || fadeOut > 0 {
			tags += fmt.Sprintf("\\fad(%d,%d)", fadeIn, fadeOut)
		}
		alignment, x := 5, layer.X+layer.Width/2
		switch alignments[i] {
		case "left":
			alignment, x = 4, layer.X
		case "right":
			alignment, x = 6, layer.X+layer.Width
		}
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,{\\an%d\\pos(%d,%d)\\fn%s\\fs%d\\c%s\\b%d%s}%s\n", assTime(0), assTime(scene.Duration), alignment, x, layer.Y+layer.Height/2, font, layer.FontSize, assColor(layer.Color), bold, tags, value)
	}
	return b.String()
}
