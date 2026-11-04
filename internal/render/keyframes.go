package render

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Animatable properties. Positions are pixel offsets from the element's rest
// position, rotation is in degrees clockwise, and scale multiplies the size
// around the element's center. The reveal properties (text only) count the
// characters and lines of a text: a number, or a percentage of the total
// written as a string ("100%"). Width and height (shapes and images) are
// sizes in pixels around the element's center; unlike scale, they keep corner
// radii and image detail as they are.
var keyframeProperties = []string{"x", "y", "scale", "scale_x", "scale_y", "rotation", "opacity", "volume",
	"width", "height", "chars_start", "chars_end", "lines_start", "lines_end"}

// revealProps are the text reveal properties.
var revealProps = []string{"chars_start", "chars_end", "lines_start", "lines_end"}

// unitProps are the properties limited to 0–1.
var unitProps = map[string]bool{"opacity": true, "volume": true}

// sizeProps are the properties that can't be negative.
var sizeProps = map[string]bool{"width": true, "height": true}

var easings = map[string]bool{
	"linear": true, "step": true, "ease-in": true, "ease-out": true, "ease-in-out": true,
	"back-in": true, "back-out": true, "elastic-out": true, "bounce-out": true,
}

// Keyframe is one resolved value of a single property.
type Keyframe struct {
	At    time.Duration
	Value float64
	// Ease shapes the segment from this keyframe to the next one.
	Ease string
	// Relative marks a reveal value written as a percentage: Value is then a
	// fraction of the text's total, resolved when the text is rendered.
	Relative bool
}

// Animation holds keyframed properties relative to the start of their owner
// (an element's scene or a track clip).
type Animation struct {
	Props map[string][]Keyframe
}

// Has reports whether any of the named properties are animated.
func (a *Animation) Has(names ...string) bool {
	if a == nil {
		return false
	}
	for _, name := range names {
		if len(a.Props[name]) > 0 {
			return true
		}
	}
	return false
}

// Value evaluates a property at t, falling back to def when it is not animated.
func (a *Animation) Value(name string, t time.Duration, def float64) float64 {
	return a.ValueOf(name, t, def, 1)
}

// ValueOf is Value with relative (percentage) keyframes scaled by total.
func (a *Animation) ValueOf(name string, t time.Duration, def, total float64) float64 {
	if a == nil || len(a.Props[name]) == 0 {
		return def
	}
	frames := a.Props[name]
	value := func(f Keyframe) float64 {
		if f.Relative {
			return f.Value * total
		}
		return f.Value
	}
	if t <= frames[0].At {
		return value(frames[0])
	}
	for i := 0; i < len(frames)-1; i++ {
		from, to := frames[i], frames[i+1]
		if t < to.At {
			p := float64(t-from.At) / float64(to.At-from.At)
			return value(from) + (value(to)-value(from))*ease(from.Ease, p)
		}
	}
	return value(frames[len(frames)-1])
}

// revealValue parses a reveal property: a count of at least 0, or a
// percentage string ("50%"), returned as a fraction with relative set.
func revealValue(v any) (value float64, relative bool, err error) {
	if s, ok := v.(string); ok {
		number, found := strings.CutSuffix(strings.TrimSpace(s), "%")
		pct, err := strconv.ParseFloat(number, 64)
		if !found || err != nil || pct < 0 {
			return 0, false, fmt.Errorf("%q is neither a count nor a percentage like \"50%%\"", s)
		}
		return pct / 100, true, nil
	}
	n, ok := number(v)
	if !ok || n < 0 {
		return 0, false, fmt.Errorf("%v must be a count of at least 0, or a percentage like \"50%%\"", v)
	}
	return n, false, nil
}

// Expr renders a property as an FFmpeg expression of tvar (seconds since the
// owner started). Unanimated properties become the constant def.
func (a *Animation) Expr(name, tvar string, def float64) string {
	if a == nil || len(a.Props[name]) == 0 {
		return num6(def)
	}
	frames := a.Props[name]
	expr := num6(frames[len(frames)-1].Value)
	for i := len(frames) - 2; i >= 0; i-- {
		from, to := frames[i], frames[i+1]
		if to.At == from.At {
			continue
		}
		p := fmt.Sprintf("((%s-%s)/%s)", tvar, num6(from.At.Seconds()), num6((to.At - from.At).Seconds()))
		segment := fmt.Sprintf("(%s+%s*%s)", num6(from.Value), num6(to.Value-from.Value), easeExpr(from.Ease, p))
		expr = fmt.Sprintf("if(lt(%s,%s),%s,%s)", tvar, num6(to.At.Seconds()), segment, expr)
	}
	return fmt.Sprintf("if(lt(%s,%s),%s,%s)", tvar, num6(frames[0].At.Seconds()), num6(frames[0].Value), expr)
}

// reaches0 reports whether any of the named properties has a keyframe at 0.
func (a *Animation) reaches0(names ...string) bool {
	if a == nil {
		return false
	}
	for _, name := range names {
		for _, f := range a.Props[name] {
			if f.Value == 0 {
				return true
			}
		}
	}
	return false
}

// End returns the time of the last keyframe across all properties.
func (a *Animation) End() time.Duration {
	end := time.Duration(0)
	if a == nil {
		return end
	}
	for _, frames := range a.Props {
		end = max(end, frames[len(frames)-1].At)
	}
	return end
}

func ease(kind string, p float64) float64 {
	p = math.Max(0, math.Min(1, p))
	const c1, c3, c4 = 1.70158, 2.70158, 2 * math.Pi / 3
	switch kind {
	case "step":
		return 0
	case "ease-in":
		return p * p * p
	case "ease-out":
		return 1 - math.Pow(1-p, 3)
	case "ease-in-out":
		if p < 0.5 {
			return 4 * p * p * p
		}
		return 1 - math.Pow(-2*p+2, 3)/2
	case "back-in":
		return c3*p*p*p - c1*p*p
	case "back-out":
		return 1 + c3*math.Pow(p-1, 3) + c1*math.Pow(p-1, 2)
	case "elastic-out":
		if p == 0 || p == 1 {
			return p
		}
		return math.Pow(2, -10*p)*math.Sin((p*10-0.75)*c4) + 1
	case "bounce-out":
		const n1, d1 = 7.5625, 2.75
		switch {
		case p < 1/d1:
			return n1 * p * p
		case p < 2/d1:
			p -= 1.5 / d1
			return n1*p*p + 0.75
		case p < 2.5/d1:
			p -= 2.25 / d1
			return n1*p*p + 0.9375
		default:
			p -= 2.625 / d1
			return n1*p*p + 0.984375
		}
	default:
		return p
	}
}

// easeExpr mirrors ease as an FFmpeg expression of the segment progress p.
func easeExpr(kind, p string) string {
	switch kind {
	case "step":
		return "0"
	case "ease-in":
		return fmt.Sprintf("pow(%s,3)", p)
	case "ease-out":
		return fmt.Sprintf("(1-pow(1-%s,3))", p)
	case "ease-in-out":
		return fmt.Sprintf("if(lt(%[1]s,0.5),4*pow(%[1]s,3),1-pow(-2*%[1]s+2,3)/2)", p)
	case "back-in":
		return fmt.Sprintf("(2.70158*pow(%[1]s,3)-1.70158*pow(%[1]s,2))", p)
	case "back-out":
		return fmt.Sprintf("(1+2.70158*pow(%[1]s-1,3)+1.70158*pow(%[1]s-1,2))", p)
	case "elastic-out":
		return fmt.Sprintf("if(gte(%[1]s,1),1,pow(2,-10*%[1]s)*sin((%[1]s*10-0.75)*2.094395)+1)", p)
	case "bounce-out":
		return fmt.Sprintf("if(lt(%[1]s,0.363636),7.5625*pow(%[1]s,2),if(lt(%[1]s,0.727273),7.5625*pow(%[1]s-0.545455,2)+0.75,if(lt(%[1]s,0.909091),7.5625*pow(%[1]s-0.818182,2)+0.9375,7.5625*pow(%[1]s-0.954545,2)+0.984375)))", p)
	default:
		return p
	}
}

// timePoint parses a non-negative offset ("1.5s", 1.5) or an offset counted
// back from the owner's end ("-0.5s").
func timePoint(v any) (time.Duration, bool, error) {
	if s, ok := v.(string); ok && strings.HasPrefix(s, "-") {
		s = strings.TrimPrefix(s, "-")
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64); err == nil && n == 0 {
			return 0, true, nil
		}
		d, err := duration(s)
		return d, true, err
	}
	d, err := offset(v)
	return d, false, err
}

func resolvePoint(v any, length time.Duration) (time.Duration, error) {
	d, fromEnd, err := timePoint(v)
	if err != nil {
		return 0, err
	}
	if fromEnd {
		d = length - d
	}
	if d < 0 || d > length {
		return 0, fmt.Errorf("time %v is outside 0–%s", v, length)
	}
	return d, nil
}

// parseKeyframes resolves a keyframe list against the owner's length. Each
// keyframe sets any subset of properties; a property animates between the
// keyframes that mention it.
func parseKeyframes(raw any, length time.Duration, allowed map[string]bool) (*Animation, error) {
	list, _ := raw.([]any)
	if len(list) == 0 {
		return nil, nil
	}
	anim := &Animation{Props: map[string][]Keyframe{}}
	for i, item := range list {
		kf := object(item)
		at, err := resolvePoint(kf["at"], length)
		if err != nil {
			return nil, fmt.Errorf("keyframe %d: %w", i, err)
		}
		easing := str(choose(kf["ease"], "linear"))
		if !easings[easing] {
			return nil, fmt.Errorf("keyframe %d: unsupported ease %q", i, easing)
		}
		for _, name := range keyframeProperties {
			value, ok := kf[name]
			if !ok {
				continue
			}
			if !allowed[name] {
				return nil, fmt.Errorf("keyframe %d: property %q cannot be animated here", i, name)
			}
			frame := Keyframe{At: at, Value: num(value), Ease: easing}
			if slices.Contains(revealProps, name) {
				if frame.Value, frame.Relative, err = revealValue(value); err != nil {
					return nil, fmt.Errorf("keyframe %d: %s: %w", i, name, err)
				}
			}
			anim.Props[name] = append(anim.Props[name], frame)
		}
	}
	for name, frames := range anim.Props {
		sort.SliceStable(frames, func(i, j int) bool { return frames[i].At < frames[j].At })
		for _, f := range frames {
			if unitProps[name] && (f.Value < 0 || f.Value > 1) {
				return nil, fmt.Errorf("%s keyframes must be between 0 and 1", name)
			}
			if sizeProps[name] && f.Value < 0 {
				return nil, fmt.Errorf("%s keyframes must be at least 0", name)
			}
		}
	}
	if len(anim.Props) == 0 {
		return nil, nil
	}
	return anim, nil
}

// num6 formats a number for FFmpeg expressions; negatives are parenthesized so
// they can follow any operator.
func num6(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	if v == math.Trunc(v) && math.Abs(v) < 1e12 {
		s = strconv.FormatFloat(v, 'f', 0, 64)
	}
	if v < 0 {
		return "(" + s + ")"
	}
	return s
}
