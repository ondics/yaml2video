package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"reflect"
	"regexp"

	"strings"
	"time"

	yaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/ondics/yaml2video/docs"
)

// transitions maps built-in transition names to FFmpeg xfade transitions.
var transitions = map[string]string{
	"fade": "fade", "dissolve": "dissolve", "wipe-left": "wipeleft", "wipe-right": "wiperight",
	"wipe-up": "wipeup", "wipe-down": "wipedown", "slide-left": "slideleft",
	"slide-right": "slideright", "slide-up": "slideup", "slide-down": "slidedown",
	"circle-open": "circleopen", "circle-close": "circleclose", "radial": "radial",
	"pixelize": "pixelize", "zoom-in": "zoomin", "cover": "coverleft", "reveal": "revealleft", "blur": "hblur",

	"fade-black": "fadeblack", "fade-white": "fadewhite", "fade-grays": "fadegrays",
	"fade-fast": "fadefast", "fade-slow": "fadeslow", "distance": "distance",
	"smooth-left": "smoothleft", "smooth-right": "smoothright", "smooth-up": "smoothup", "smooth-down": "smoothdown",
	"circle-crop": "circlecrop", "rect-crop": "rectcrop",
	"horz-open": "horzopen", "horz-close": "horzclose", "vert-open": "vertopen", "vert-close": "vertclose",
	"diag-tl": "diagtl", "diag-tr": "diagtr", "diag-bl": "diagbl", "diag-br": "diagbr",
	"wipe-tl": "wipetl", "wipe-tr": "wipetr", "wipe-bl": "wipebl", "wipe-br": "wipebr",
	"slice-left": "hlslice", "slice-right": "hrslice", "slice-up": "vuslice", "slice-down": "vdslice",
	"squeeze-h": "squeezeh", "squeeze-v": "squeezev",
	"wind-left": "hlwind", "wind-right": "hrwind", "wind-up": "vuwind", "wind-down": "vdwind",
	"cover-left": "coverleft", "cover-right": "coverright", "cover-up": "coverup", "cover-down": "coverdown",
	"reveal-left": "revealleft", "reveal-right": "revealright", "reveal-up": "revealup", "reveal-down": "revealdown",
}

// xfadeNames holds every xfade transition a plan may use, including custom expressions.
var xfadeNames = func() map[string]bool {
	names := map[string]bool{"custom": true}
	for _, xfade := range transitions {
		names[xfade] = true
	}
	return names
}()

func (p *Plan) validateRenderPlan() error {
	if p.Duration <= 0 {
		return fmt.Errorf("plan duration must be positive")
	}

	for i, scene := range p.Scenes {
		if scene.Duration <= 0 {
			return fmt.Errorf("scene %d: duration must be positive", i)
		}

		for j, layer := range scene.Layers {
			if err := validateEffects(layer, scene.Duration); err != nil {
				return fmt.Errorf("scene %d layer %d: %w", i, j, err)
			}
		}

		for j, audio := range scene.Audio {
			remaining := scene.Duration - audio.Offset
			duration := mediaDuration(audio.Duration, remaining)
			if audio.Offset < 0 || remaining <= 0 || duration <= 0 || duration > remaining || audio.SourceOffset < 0 {
				return fmt.Errorf("scene %d audio %d: offset and duration must fit within the scene", i, j)
			}
			if err := validateFades(audio.FadeIn, audio.FadeOut, duration); err != nil {
				return fmt.Errorf("scene %d audio %d: %w", i, j, err)
			}
		}
	}

	for _, tr := range p.Transitions {
		if tr.FromScene < 0 || tr.FromScene >= len(p.Scenes)-1 {
			return fmt.Errorf("transition: invalid scene index %d", tr.FromScene)
		}

		if tr.Type == "cut" || tr.XFade == "" && tr.Duration == 0 {
			if tr.Duration != 0 {
				return fmt.Errorf("cut transition must have zero duration")
			}
			continue
		}

		if !xfadeNames[tr.xfade()] {
			return fmt.Errorf("unsupported scene transition %q", tr.Type)
		}
		if tr.xfade() == "custom" && tr.Expr == "" {
			return fmt.Errorf("custom transition %q needs an expression", tr.Type)
		}

		if tr.Duration <= 0 || tr.Duration > p.Scenes[tr.FromScene].Duration || tr.Duration > p.Scenes[tr.FromScene+1].Duration {
			return fmt.Errorf("transition %q must fit within both scenes", tr.Type)
		}
	}

	if p.Music != nil {
		if err := validateFades(p.Music.FadeIn, p.Music.FadeOut, p.Duration); err != nil {
			return fmt.Errorf("music: %w", err)
		}

		if d := p.Music.Ducking; d != nil && d.Enabled {
			if d.Amount < 0 || d.Amount > 1 || d.Attack < 0 || d.Release < 0 {
				return fmt.Errorf("music ducking: invalid amount, attack, or release")
			}
			// FFmpeg sidechaincompress accepts attack 0.01-2000 ms and release 0.01-9000 ms.
			if d.Attack < 10*time.Microsecond || d.Attack > 2*time.Second || d.Release < 10*time.Microsecond || d.Release > 9*time.Second {
				return fmt.Errorf("music ducking: attack must be 0.01ms–2s and release 0.01ms–9s")
			}
		}
	}
	return nil
}

func validateFades(in, out, duration time.Duration) error {
	if in < 0 || out < 0 || in > duration || out > duration {
		return fmt.Errorf("fade durations must fit within track duration")
	}
	return nil
}

// Schema validation operates on JSON values so YAML scalar types are not coerced
// by Go struct decoding. Validation uses schemas embedded in the binary.
func document(path, schemaName string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return documentBytes(raw, path, schemaName)
}

func documentBytes(raw []byte, path, schemaName string) (map[string]any, error) {
	parsed, err := parser.ParseBytes(raw, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: YAML: %w", path, err)
	}
	if len(parsed.Docs) != 1 {
		return nil, fmt.Errorf("%s: expected one YAML document, got %d", path, len(parsed.Docs))
	}

	var node any
	if err = yaml.UnmarshalWithOptions(raw, &node, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s: YAML: %w", path, err)
	}

	b, err := yaml.YAMLToJSON(raw)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var value any
	if err = dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected an object", path)
	}
	// This field is an editor hint, not a runtime schema selector.
	delete(result, "$schema")

	schemaBytes, err := docs.Schemas.ReadFile(schemaName)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", schemaName, err)
	}

	var schema map[string]any
	if err = json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, err
	}

	if err = validate(value, schema, schema, "$"); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return result, nil
}

func validate(value any, rule, root map[string]any, path string) error {
	fail := func(msg string) error { return fmt.Errorf("%s: %s", path, msg) }
	if ref, ok := rule["$ref"].(string); ok {
		cur := any(root)
		for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			obj, ok := cur.(map[string]any)
			if !ok {
				return fail("invalid schema reference")
			}
			cur = obj[part]
		}
		target, ok := cur.(map[string]any)
		if !ok {
			return fail("invalid schema reference")
		}
		return validate(value, target, root, path)
	}

	if typ, ok := rule["type"].(string); ok {
		valid := false
		switch typ {
		case "object":
			_, valid = value.(map[string]any)
		case "array":
			_, valid = value.([]any)
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number":
			if _, ok := value.(json.Number); ok {
				_, valid = number(value)
			}
		case "integer":
			n, ok := value.(json.Number)
			if ok {
				f, e := n.Float64()
				valid = e == nil && !math.IsInf(f, 0) && math.Trunc(f) == f
			}
		}

		if !valid {
			return fail("expected " + typ)
		}
	}

	if c, ok := rule["const"]; ok && !reflect.DeepEqual(normal(value), normal(c)) {
		return fail("invalid constant")
	}

	if enums, ok := rule["enum"].([]any); ok {
		found := false
		for _, e := range enums {
			if reflect.DeepEqual(normal(value), normal(e)) {
				found = true
			}
		}
		if !found {
			return fail("not in enum")
		}
	}

	if s, ok := value.(string); ok {
		if min, ok := rule["minLength"].(float64); ok && len([]rune(s)) < int(min) {
			return fail("string too short")
		}

		if pat, ok := rule["pattern"].(string); ok {
			re, e := regexp.Compile(pat)
			if e != nil {
				return e
			}
			if !re.MatchString(s) {
				return fail("pattern mismatch")
			}
		}
	}

	if n, ok := number(value); ok {
		for _, bound := range []string{"minimum", "exclusiveMinimum", "maximum", "exclusiveMaximum"} {
			if b, ok := rule[bound].(float64); ok {
				switch bound {
				case "minimum":
					if n < b {
						return fail(bound)
					}
				case "exclusiveMinimum":
					if n <= b {
						return fail(bound)
					}
				case "maximum":
					if n > b {
						return fail(bound)
					}
				case "exclusiveMaximum":
					if n >= b {
						return fail(bound)
					}
				}
			}
		}
	}

	if obj, ok := value.(map[string]any); ok {
		if req, ok := rule["required"].([]any); ok {
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					return fail("missing " + r.(string))
				}
			}
		}

		props, _ := rule["properties"].(map[string]any)
		for k, v := range obj {
			r, ok := props[k].(map[string]any)
			if !ok {
				if rule["additionalProperties"] == false {
					return fail("unknown property " + k)
				}
				// A schema here validates every property not listed (map-like objects).
				if r, ok = rule["additionalProperties"].(map[string]any); !ok {
					continue
				}
			}
			if err := validate(v, r, root, path+"."+k); err != nil {
				return err
			}
		}
	}

	if arr, ok := value.([]any); ok {
		if m, ok := rule["minItems"].(float64); ok && len(arr) < int(m) {
			return fail("too few items")
		}

		if m, ok := rule["maxItems"].(float64); ok && len(arr) > int(m) {
			return fail("too many items")
		}

		if rule["uniqueItems"] == true {
			for i := range arr {
				for j := range i {
					if reflect.DeepEqual(arr[i], arr[j]) {
						return fail("duplicate item")
					}
				}
			}
		}

		if item, ok := rule["items"].(map[string]any); ok {
			for i, v := range arr {
				if err := validate(v, item, root, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}

	for _, kind := range []string{"oneOf", "anyOf", "allOf"} {
		if rules, ok := rule[kind].([]any); ok {
			passes := 0
			var firstErr, bestErr error
			bestScore := -1
			for _, r := range rules {
				branch := r.(map[string]any)
				err := validate(value, branch, root, path)
				if err == nil {
					passes++
					continue
				}
				if firstErr == nil {
					firstErr = err
				}
				if score := branchScore(value, branch, root, err); score > bestScore {
					bestErr, bestScore = err, score
				}
			}
			// allOf branches are independent requirements, so the first failure is the useful message.
			if kind == "allOf" && passes != len(rules) {
				return firstErr
			}
			if kind == "oneOf" && passes > 1 {
				return fail("matches more than one alternative")
			}
			// With no match, report the alternative the value most resembles.
			if kind == "oneOf" && passes == 0 || kind == "anyOf" && passes == 0 {
				return bestErr
			}
		}
	}

	if cond, ok := rule["if"].(map[string]any); ok {
		branch := "else"
		if validate(value, cond, root, path) == nil {
			branch = "then"
		}
		if next, ok := rule[branch].(map[string]any); ok {
			return validate(value, next, root, path)
		}
	}

	return nil
}

// branchScore rates how closely a value resembles a failed schema alternative:
// matching type first, then how many of its required properties are present,
// then how deep the error is.
func branchScore(value any, rule, root map[string]any, err error) int {
	for depth := 0; depth < 16; depth++ {
		ref, ok := rule["$ref"].(string)
		if !ok {
			break
		}
		cur := any(root)
		for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			cur = object(cur)[part]
		}
		rule = object(cur)
	}
	score := 0
	if typ, ok := rule["type"].(string); !ok || validate(value, map[string]any{"type": typ}, root, "") == nil {
		score += 1000
	}
	if obj, ok := value.(map[string]any); ok {
		for _, r := range list(rule["required"]) {
			if _, ok := obj[str(r)]; ok {
				score += 10
			}
		}
	}
	at, _, _ := strings.Cut(err.Error(), ": ")
	return score + strings.Count(at, ".") + strings.Count(at, "[")
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, e := n.Float64()
		return f, e == nil && !math.IsInf(f, 0)
	case float64:
		return n, !math.IsInf(n, 0) && !math.IsNaN(n)
	}
	return 0, false
}

func normal(v any) any {
	if n, ok := number(v); ok {
		return n
	}
	return v
}
