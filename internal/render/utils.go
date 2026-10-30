package render

import (
	"strings"

	"github.com/ondics/yaml2video/internal/types"
)

func colorString(color types.Color) string {
	value, _ := color.FFmpeg()
	return value
}

func safeName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}
