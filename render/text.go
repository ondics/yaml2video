package render

import (
	"math"
	"strings"
	"unicode"

	"github.com/ondics/yaml2video/project"
)

const estimatedGlyphWidth = 0.4

func wrapTextSpans(spans []TextSpan, width, fontSize int) []TextSpan {
	if width <= 0 || fontSize <= 0 {
		return spans
	}

	maxCharacters := max(int(float64(width)/(float64(fontSize)*estimatedGlyphWidth)), 1)

	lineLength := 0
	for index := range spans {
		spans[index].Content, lineLength = wrapText(spans[index].Content, lineLength, maxCharacters)
	}

	return spans
}

func wrapText(value string, lineLength, maxCharacters int) (string, int) {
	var result strings.Builder
	var pendingWhitespace string

	for _, token := range textTokens(value) {
		if token.isWhitespace {
			if strings.ContainsRune(token.value, '\n') {
				result.WriteByte('\n')
				lineLength = 0
				pendingWhitespace = ""
			} else if lineLength > 0 {
				pendingWhitespace = " "
			}
			continue
		}

		wordLength := len([]rune(token.value))
		if lineLength > 0 && lineLength+len(pendingWhitespace)+wordLength > maxCharacters {
			result.WriteByte('\n')
			lineLength = 0
			pendingWhitespace = ""
		}

		if pendingWhitespace != "" && lineLength > 0 {
			result.WriteString(pendingWhitespace)
			lineLength += len(pendingWhitespace)
		}

		result.WriteString(token.value)
		lineLength += wordLength
		pendingWhitespace = ""
	}

	return result.String(), lineLength
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

func measureText(spans []TextSpan, fontSize int) textMetrics {
	lineHeight := max(int(math.Round(float64(fontSize)*1.2)), 1)

	lineWidths := []int{0}
	for _, span := range spans {
		lines := strings.Split(span.Content, "\n")
		for index, line := range lines {
			lineWidths[len(lineWidths)-1] += textWidth(line, fontSize)
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

func textItemBackgrounds(spans []TextSpan, metrics textMetrics, layer LayerPlan) []TextBackgroundBox {
	boxes := make([]TextBackgroundBox, 0)
	line := 0
	lineOffset := 0
	textTop := layer.Y + (layer.Height-metrics.Height)/2

	for _, span := range spans {
		lines := strings.Split(span.Content, "\n")
		for index, value := range lines {
			width := textWidth(value, layer.FontSize)
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

func textWidth(value string, fontSize int) int {
	width := int(math.Round(float64(len([]rune(value))) * float64(fontSize) * estimatedGlyphWidth))
	if width < 1 && value != "" {
		return 1
	}

	return width
}
