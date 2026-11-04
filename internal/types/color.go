package types

import (
	"fmt"
	"math"
	"strings"
)

type Color string

// Parse parses a color string (hex, rgb() or rgba()) and outputs
// the red, green, blue and alpha values as floats in the range [0, 1].
func (c Color) Parse() (r, g, b, a float64) {
	str := strings.ToLower(strings.TrimSpace(string(c)))
	if named, ok := namedColors[str]; ok {
		return named.parseHex(string(named))
	}
	if len(str) > 0 && str[0] == '#' {
		return c.parseHex(str)
	} else if len(str) > 0 && strings.HasPrefix(str, "rgb") {
		return c.parseRGB(str)
	}
	return
}

var namedColors = map[string]Color{
	"black": "#000000", "white": "#ffffff", "red": "#ff0000",
	"green": "#008000", "blue": "#0000ff", "yellow": "#ffff00",
	"transparent": "#00000000",
}

// FFmpeg returns an FFmpeg color expression (0xRRGGBB@opacity) after
// validating the YAML-facing color syntax.
func (c Color) FFmpeg() (string, error) {
	value := strings.ToLower(strings.TrimSpace(string(c)))
	if named, ok := namedColors[value]; ok {
		value = string(named)
	}
	if !strings.HasPrefix(value, "#") && !strings.HasPrefix(value, "rgb") {
		return "", fmt.Errorf("unsupported color %q", c)
	}
	r, g, b, a := Color(value).Parse()
	if math.IsNaN(r) || math.IsNaN(g) || math.IsNaN(b) || math.IsNaN(a) || r < 0 || r > 1 || g < 0 || g > 1 || b < 0 || b > 1 || a < 0 || a > 1 {
		return "", fmt.Errorf("invalid color %q", c)
	}
	// Parse intentionally returns zero values for malformed input, so reject
	// non-black formats that decode to transparent black.
	if r == 0 && g == 0 && b == 0 && a == 0 && value != "#00000000" && value != "rgba(0,0,0,0)" {
		return "", fmt.Errorf("invalid color %q", c)
	}
	return fmt.Sprintf("0x%02X%02X%02X@%g", int(math.Round(r*255)), int(math.Round(g*255)), int(math.Round(b*255)), a), nil
}

func (c Color) parseHex(s string) (r, g, b, a float64) {
	hex := func(ch byte) (float64, bool) {
		switch {
		case ch >= '0' && ch <= '9':
			return float64(ch - '0'), true
		case ch >= 'a' && ch <= 'f':
			return float64(ch-'a') + 10, true
		case ch >= 'A' && ch <= 'F':
			return float64(ch-'A') + 10, true
		}
		return 0, false
	}

	h := s[1:]
	if len(h) == 3 || len(h) == 4 {
		values := [4]float64{0, 0, 0, 15}
		for i := range h {
			v, ok := hex(h[i])
			if !ok {
				return
			}
			values[i] = v
		}
		r, g, b, a = values[0]/15, values[1]/15, values[2]/15, values[3]/15
		return
	}
	if len(h) == 6 || len(h) == 8 {
		values := [4]float64{0, 0, 0, 255}
		for i := 0; i < len(h); i += 2 {
			hi, ok1 := hex(h[i])
			lo, ok2 := hex(h[i+1])
			if !ok1 || !ok2 {
				return
			}
			values[i/2] = hi*16 + lo
		}
		r, g, b, a = values[0]/255, values[1]/255, values[2]/255, values[3]/255
		return
	}
	return
}

func (c Color) parseRGB(s string) (r, g, b, a float64) {
	isRGB := len(s) >= 4 && (s[0] == 'r' || s[0] == 'R') && (s[1] == 'g' || s[1] == 'G') && (s[2] == 'b' || s[2] == 'B')
	if !isRGB {
		return
	}
	pos, hasAlpha := 3, false
	if len(s) >= 5 && (s[3] == 'a' || s[3] == 'A') {
		pos, hasAlpha = 4, true
	}
	if pos >= len(s) || s[pos] != '(' || s[len(s)-1] != ')' {
		return
	}
	body := s[pos+1 : len(s)-1]
	values := [4]float64{0, 0, 0, 1}
	percents := [4]bool{}
	count := 0
	for i := 0; i < len(body); {
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == ',' || body[i] == '/') {
			i++
		}
		if i == len(body) || count == 4 {
			break
		}
		sign := 1.0
		if body[i] == '-' || body[i] == '+' {
			if body[i] == '-' {
				sign = -1
			}
			i++
		}
		value, divisor := 0.0, 1.0
		digits := false
		for i < len(body) && body[i] >= '0' && body[i] <= '9' {
			value = value*10 + float64(body[i]-'0')
			i++
			digits = true
		}
		if i < len(body) && body[i] == '.' {
			i++
			for i < len(body) && body[i] >= '0' && body[i] <= '9' {
				value = value*10 + float64(body[i]-'0')
				divisor *= 10
				i++
				digits = true
			}
		}
		if !digits {
			return
		}
		values[count] = sign * value / divisor
		if i < len(body) && body[i] == '%' {
			percents[count] = true
			i++
		}
		count++
	}
	if count != 3 && !(hasAlpha && count == 4) {
		return
	}
	if percents[0] {
		r = values[0] / 100
	} else {
		r = values[0] / 255
	}
	if percents[1] {
		g = values[1] / 100
	} else {
		g = values[1] / 255
	}
	if percents[2] {
		b = values[2] / 100
	} else {
		b = values[2] / 255
	}
	if hasAlpha {
		a = values[3]
		if percents[3] {
			a /= 100
		}
	} else {
		a = 1
	}

	return
}
