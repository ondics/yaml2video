package render

import (
	"fmt"
	"regexp"
	"time"
)

// A scene boundary is resolved from the outgoing scene's out transition and
// the incoming scene's in transition. When only one is given it covers both
// sides; with neither, the boundary is a plain cut.
//
// A transition is a built-in name (an FFmpeg xfade blend) or a custom
// definition from advanced.transitions with an optional blend (base or expr)
// and effect layers for the whole transition, its out side (the end of the
// outgoing scene), and its in side (the start of the incoming scene).

var progressRe = regexp.MustCompile(`\bP\b`)

type transitionSpec struct {
	name     string
	xfade    string // xfade transition; empty for a cut
	expr     string // xfade custom expression, with progress already rewritten
	duration time.Duration
	shared   obj // effect parts as written; parsed once their length is known
	out      obj
	in       obj
}

type transitionResolver struct {
	defs     obj // custom transition definitions
	presets  *advancedContext
	override obj // video advanced.scene_transitions
}

func newTransitionResolver(v, t obj, presets *advancedContext) *transitionResolver {
	tadv, vadv := object(t["advanced"]), object(v["advanced"])
	return &transitionResolver{
		defs:     merge(object(tadv["transitions"]), object(vadv["transitions"])),
		presets:  presets,
		override: object(vadv["scene_transitions"]),
	}
}

// pick selects a section's transition for its i-th scene; lists rotate.
func pick(v any, i int) obj {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return object(l[i%len(l)])
	}
	return object(v)
}

func (r *transitionResolver) resolve(use obj) (*transitionSpec, error) {
	name := str(use["type"])
	if name == "" {
		return nil, nil
	}
	var useDuration time.Duration
	if use["duration"] != nil {
		d, err := duration(use["duration"])
		if err != nil {
			return nil, fmt.Errorf("transition %q: %w", name, err)
		}
		useDuration = d
	}

	if name == "cut" {
		return &transitionSpec{name: name}, nil
	}
	if xfade, ok := transitions[name]; ok {
		if useDuration == 0 {
			return nil, fmt.Errorf("built-in transition %q needs a duration", name)
		}
		return &transitionSpec{name: name, xfade: xfade, duration: useDuration}, nil
	}

	def := object(r.defs[name])
	if len(def) == 0 {
		return nil, fmt.Errorf("unknown transition %q", name)
	}
	spec := &transitionSpec{name: name, duration: useDuration, out: object(def["out"]), in: object(def["in"])}
	if spec.duration == 0 && def["duration"] != nil {
		d, err := duration(def["duration"])
		if err != nil {
			return nil, fmt.Errorf("transition %q: %w", name, err)
		}
		spec.duration = d
	}
	if base := str(def["base"]); base != "" && base != "cut" {
		xfade, ok := transitions[base]
		if !ok {
			return nil, fmt.Errorf("transition %q: base %q is not a built-in transition", name, base)
		}
		spec.xfade = xfade
	}
	if expr := str(def["expr"]); expr != "" {
		if spec.xfade != "" {
			return nil, fmt.Errorf("transition %q: use base or expr, not both", name)
		}
		// Authors write P as progress 0→1; xfade counts it down from 1.
		spec.xfade, spec.expr = "custom", progressRe.ReplaceAllString(expr, "(1-P)")
	}
	if def["effects"] != nil || def["keyframes"] != nil {
		spec.shared = def
	}
	if spec.duration == 0 && (spec.xfade != "" || spec.shared != nil) {
		return nil, fmt.Errorf("transition %q needs a duration", name)
	}
	return spec, nil
}

// effectPart builds an effect clip for one side of a transition. Its length
// is the part's own duration or the transition's.
func (r *transitionResolver) effectPart(part obj, fallback time.Duration, where string) (ClipPlan, error) {
	length := fallback
	if part["duration"] != nil {
		d, err := duration(part["duration"])
		if err != nil {
			return ClipPlan{}, fmt.Errorf("%s: %w", where, err)
		}
		length = d
	}
	if length <= 0 {
		return ClipPlan{}, fmt.Errorf("%s needs a duration", where)
	}
	clip, err := r.presets.effectClip(part, length)
	if err != nil {
		return ClipPlan{}, fmt.Errorf("%s: %w", where, err)
	}
	return clip, nil
}

// boundaryPlan is a resolved boundary: the blend between the scenes plus effect
// clips on the outgoing scene's tail, the incoming scene's head, and the final
// timeline around the boundary (start relative to the boundary's center).
type boundaryPlan struct {
	transition BoundaryTransition
	out, in    []ClipPlan
	shared     []ClipPlan
}

func (r *transitionResolver) boundary(from, to ScenePlan, index int, out, in obj) (*boundaryPlan, error) {
	if o := object(object(r.override[from.ID])["out"]); len(o) > 0 {
		out = o
	}
	if o := object(object(r.override[to.ID])["in"]); len(o) > 0 {
		in = o
	}
	outSpec, err := r.resolve(out)
	if err != nil {
		return nil, err
	}
	inSpec, err := r.resolve(in)
	if err != nil {
		return nil, err
	}
	if outSpec == nil && inSpec == nil {
		return nil, nil
	}
	// One transition covers both sides unless the other side has its own.
	if outSpec == nil {
		outSpec = inSpec
	}
	if inSpec == nil {
		inSpec = outSpec
	}

	blend := outSpec
	if blend.xfade == "" && inSpec.xfade != "" {
		blend = inSpec
	}
	b := &boundaryPlan{transition: BoundaryTransition{FromScene: index, Type: blend.name, XFade: blend.xfade, Expr: blend.expr}}
	if blend.xfade != "" {
		b.transition.Duration = blend.duration
		if blend.duration >= from.Duration || blend.duration >= to.Duration {
			return nil, fmt.Errorf("transition %q exceeds adjacent scene duration", blend.name)
		}
	}
	if outSpec.name != inSpec.name {
		b.transition.Type = outSpec.name + " → " + inSpec.name
	}

	if len(outSpec.out) > 0 {
		clip, err := r.effectPart(outSpec.out, outSpec.duration, "transition "+outSpec.name+" out")
		if err != nil {
			return nil, err
		}
		if clip.Duration > from.Duration {
			return nil, fmt.Errorf("transition %q: out side is longer than scene %q", outSpec.name, from.ID)
		}
		clip.Start = from.Duration - clip.Duration
		b.out = append(b.out, clip)
	}
	if len(inSpec.in) > 0 {
		clip, err := r.effectPart(inSpec.in, inSpec.duration, "transition "+inSpec.name+" in")
		if err != nil {
			return nil, err
		}
		if clip.Duration > to.Duration {
			return nil, fmt.Errorf("transition %q: in side is longer than scene %q", inSpec.name, to.ID)
		}
		b.in = append(b.in, clip)
	}
	for _, spec := range uniqueSpecs(outSpec, inSpec) {
		if spec.shared == nil {
			continue
		}
		clip, err := r.effectPart(spec.shared, spec.duration, "transition "+spec.name)
		if err != nil {
			return nil, err
		}
		b.shared = append(b.shared, clip)
	}
	return b, nil
}

// placeTransitionEffects centers each boundary's shared effect layers on the
// blend (or on the cut) once scene start times are known.
func placeTransitionEffects(p *Plan, boundaries []*boundaryPlan) error {
	for i, b := range boundaries {
		tr := &p.Transitions[i]
		center := tr.Offset + tr.Duration/2
		for _, clip := range b.shared {
			clip.Start = center - clip.Duration/2
			if clip.Start < 0 || clip.Start+clip.Duration > p.Duration {
				return fmt.Errorf("transition %q: its effects extend beyond the video", tr.Type)
			}
			tr.Effects = append(tr.Effects, clip)
		}
	}
	return nil
}

func uniqueSpecs(a, b *transitionSpec) []*transitionSpec {
	if a.name == b.name {
		return []*transitionSpec{a}
	}
	return []*transitionSpec{a, b}
}
