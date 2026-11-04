package render

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/ondics/yaml2video/internal/project"
)

// textFaces are the regular and bold faces that measure and draw text and
// subtitles; bold is the regular face when no span is bold.
type textFaces struct{ regular, bold *fontFace }

func (f textFaces) of(bold bool) *fontFace {
	if bold {
		return f.bold
	}
	return f.regular
}

// resolveTextFaces resolves family for spans drawn at size pixels (0 for the
// font's default). An empty family is the bundled Go font.
func resolveTextFaces(family string, size float64, spans []TextSpan) (textFaces, error) {
	if family == "" {
		family = "Go"
	}
	regular, err := defaultFonts.face(family, 400, size)
	if err != nil {
		return textFaces{}, err
	}
	faces := textFaces{regular, regular}
	if slices.ContainsFunc(spans, func(s TextSpan) bool { return s.Bold }) {
		if faces.bold, err = defaultFonts.face(family, 700, size); err != nil {
			return textFaces{}, err
		}
	}
	return faces, nil
}

// wrapTextSpans wraps spans to width, measuring words with the font that
// draws them.
func wrapTextSpans(spans []TextSpan, width int, faces textFaces, fontSize int) []TextSpan {
	if width <= 0 || fontSize <= 0 {
		return spans
	}

	lineWidth := 0.0
	for index := range spans {
		face := faces.of(spans[index].Bold)
		measure := func(s string) float64 { return textWidthF(face, s, fontSize) }
		spans[index].Content, lineWidth = wrapText(spans[index].Content, lineWidth, float64(width), measure)
	}

	return spans
}

func wrapText(value string, lineWidth, width float64, measure func(string) float64) (string, float64) {
	var result strings.Builder
	var pendingWhitespace string

	for _, token := range textTokens(value) {
		if token.isWhitespace {
			if strings.ContainsRune(token.value, '\n') {
				result.WriteByte('\n')
				lineWidth = 0
				pendingWhitespace = ""
			} else if lineWidth > 0 {
				pendingWhitespace = " "
			}
			continue
		}

		wordWidth := measure(token.value)
		space := 0.0
		if pendingWhitespace != "" {
			space = measure("a"+pendingWhitespace+"a") - measure("aa")
		}
		if lineWidth > 0 && lineWidth+space+wordWidth > width {
			result.WriteByte('\n')
			lineWidth = 0
			pendingWhitespace = ""
		}

		if pendingWhitespace != "" && lineWidth > 0 {
			result.WriteString(pendingWhitespace)
			lineWidth += space
		}

		result.WriteString(token.value)
		lineWidth += wordWidth
		pendingWhitespace = ""
	}

	return result.String(), lineWidth
}

type textToken struct {
	value        string
	isWhitespace bool
}

func textTokens(value string) []textToken {
	if value == "" {
		return nil
	}

	tokens := make([]textToken, 0)
	start := 0
	whitespace := false
	for index, character := range value {
		isWhitespace := unicode.IsSpace(character)
		if index == 0 {
			whitespace = isWhitespace
			continue
		}
		if isWhitespace != whitespace {
			tokens = append(tokens, textToken{value: value[start:index], isWhitespace: whitespace})
			start = index
			whitespace = isWhitespace
		}
	}
	tokens = append(tokens, textToken{value: value[start:], isWhitespace: whitespace})

	return tokens
}

type textMetrics struct {
	LineWidths []int
	Width      int
	Height     int
	LineHeight int
}

func textLayout(layout project.LayerLayout, metrics textMetrics) project.LayerLayout {
	result := layout
	if result.Width == nil {
		width := metrics.Width
		result.Width = &width
	}
	if result.Height == nil {
		height := metrics.Height
		result.Height = &height
	}

	return result
}

func measureText(spans []TextSpan, faces textFaces, fontSize int) textMetrics {
	lineHeight := max(int(math.Round(float64(fontSize)*1.2)), 1)

	lineWidths := []int{0}
	for _, span := range spans {
		lines := strings.Split(span.Content, "\n")
		for index, line := range lines {
			lineWidths[len(lineWidths)-1] += textWidth(faces.of(span.Bold), line, fontSize)
			if index < len(lines)-1 {
				lineWidths = append(lineWidths, 0)
			}
		}
	}

	width := 1
	for _, lineWidth := range lineWidths {
		if lineWidth > width {
			width = lineWidth
		}
	}

	return textMetrics{
		LineWidths: lineWidths,
		Width:      width,
		Height:     len(lineWidths) * lineHeight,
		LineHeight: lineHeight,
	}
}

func textItemBackgrounds(spans []TextSpan, metrics textMetrics, layer LayerPlan, faces textFaces) []TextBackgroundBox {
	boxes := make([]TextBackgroundBox, 0)
	line := 0
	lineOffset := 0
	textTop := layer.Y + (layer.Height-metrics.Height)/2

	for _, span := range spans {
		lines := strings.Split(span.Content, "\n")
		for index, value := range lines {
			width := textWidth(faces.of(span.Bold), value, layer.FontSize)
			if span.Background != nil && width > 0 {
				lineStart := layer.X + (layer.Width-metrics.LineWidths[line])/2
				boxes = append(boxes, TextBackgroundBox{
					X:          lineStart + lineOffset - span.Background.PaddingX,
					Y:          textTop + line*metrics.LineHeight - span.Background.PaddingY,
					Width:      width + 2*span.Background.PaddingX,
					Height:     metrics.LineHeight + 2*span.Background.PaddingY,
					Background: *span.Background,
				})
			}

			lineOffset += width
			if index < len(lines)-1 {
				line++
				lineOffset = 0
			}
		}
	}

	return boxes
}

// textWidth is the width of value in face at fontSize, as libass draws it.
func textWidth(face *fontFace, value string, fontSize int) int {
	width := int(math.Round(textWidthF(face, value, fontSize)))
	if width < 1 && value != "" {
		return 1
	}

	return width
}

func textWidthF(face *fontFace, value string, fontSize int) float64 {
	x := face.offsets([]rune(value), float64(fontSize), 0, 0)
	return x[len(x)-1]
}
