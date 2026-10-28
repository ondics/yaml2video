package render

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Advanced mode is opt-in: documents without an `advanced` block compile
// exactly as before. Templates may define element animations, reusable
// keyframe and effect presets, and tracks; videos may add their own presets
// and tracks and disable template tracks. Template tracks stack below video
// tracks, each list in document order.
//
// The clip builder here is shared with custom layouts, whose elements are the
// same clips rendered as the scene's own layout.

var (
	visualProps = map[string]bool{"x": true, "y": true, "scale": true, "scale_x": true, "scale_y": true, "rotation": true, "opacity": true}
	sizedProps  = map[string]bool{"x": true, "y": true, "scale": true, "scale_x": true, "scale_y": true, "rotation": true, "opacity": true,
		"width": true, "height": true}
	textProps = map[string]bool{"x": true, "y": true, "scale": true, "scale_x": true, "scale_y": true, "rotation": true, "opacity": true,
		"chars_start": true, "chars_end": true, "lines_start": true, "lines_end": true}
	effectProps = map[string]bool{"x": true, "y": true, "scale": true, "scale_x": true, "scale_y": true, "rotation": true, "opacity": true}
	audioProps  = map[string]bool{"volume": true}
	// $(NAME), ${NAME}, either with |fallback; $$ is a literal $.
	bindingRe = regexp.MustCompile(`\$\$|\$\(([A-Za-z0-9_.-]+)(?:\|([^)]*))?\)|\$\{([A-Za-z0-9_.-]+)(?:\|([^}]*))?\}`)
)

type advancedContext struct {
	plan       *Plan
	base       string
	template   obj
	video      obj
	keyframes  obj // named keyframe presets
	effects    obj // named effect stacks
	typography obj
	theme      obj
	media      obj
	audio      obj
	folders    int // numbers folder canvases, for unique ASS file names
}

func newAdvancedContext(p *Plan, v, t obj, base string) *advancedContext {
	tadv, vadv := object(t["advanced"]), object(v["advanced"])
	defaults := object(t["defaults"])
	return &advancedContext{
		plan: p, base: base, template: t, video: v,
		keyframes:  merge(object(tadv["keyframes"]), object(vadv["keyframes"])),
		effects:    merge(object(tadv["effects"]), object(vadv["effects"])),
		typography: object(defaults["typography"]),
		theme:      object(defaults["theme"]),
		media:      object(defaults["media"]),
		audio:      object(defaults["audio"]),
	}
}

func applyAdvanced(ctx *advancedContext) error {
	p, t, v := ctx.plan, ctx.template, ctx.video
	tadv, vadv := object(t["advanced"]), object(v["advanced"])
	layoutTracks, err := ctx.layoutTracks()
	if err != nil {
		return err
	}
	if len(tadv) == 0 && len(vadv) == 0 && len(layoutTracks) == 0 {
		return nil
	}

	if master := merge(object(tadv["master"]), object(vadv["master"])); len(master) > 0 {
		p.Master = &MasterPlan{Loudness: num(choose(master["loudness"], jsonNumber(-14))), TruePeak: num(choose(master["true_peak"], jsonNumber(-1)))}
	}

	if err := ctx.animateElements(object(tadv["animate"])); err != nil {
		return err
	}

	disabled := map[string]bool{}
	for _, id := range list(vadv["disable_tracks"]) {
		disabled[str(id)] = true
	}
	seen := map[string]bool{}
	var tracks []obj
	// Layout tracks first, then the template's, then the video's.
	for _, source := range []obj{{"tracks": layoutTracks}, tadv, vadv} {
		for _, raw := range list(source["tracks"]) {
			track := object(raw)
			id := str(track["id"])
			if seen[id] {
				return fmt.Errorf("duplicate track id %q", id)
			}
			seen[id] = true
			if !disabled[id] && track["visible"] != false {
				tracks = append(tracks, track)
			}
		}
	}
	for id := range disabled {
		if !seen[id] {
			return fmt.Errorf("disable_tracks: unknown track %q", id)
		}
	}

	for i, track := range tracks {
		if err := ctx.addTrack(track, i); err != nil {
			return fmt.Errorf("track %q: %w", track["id"], err)
		}
	}
	return nil
}

// layoutTracks returns the tracks custom layouts carry, attached to their
// layout, in layout-name order.
func (c *advancedContext) layoutTracks() ([]any, error) {
	layouts := object(c.template["layout"])
	var tracks []any
	for _, name := range slices.Sorted(maps.Keys(layouts)) {
		for _, raw := range list(object(layouts[name])["tracks"]) {
			track := object(raw)
			if track["attach"] != nil {
				return nil, fmt.Errorf("layout %q: track %q: layout tracks attach to their layout; remove attach", name, track["id"])
			}
			tracks = append(tracks, merge(track, obj{"attach": name}))
		}
	}
	return tracks, nil
}

func merge(a, b obj) obj {
	out := obj{}
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

// deepMerge overlays b on a, merging nested objects and replacing other values.
func deepMerge(a, b obj) obj {
	out := merge(a, nil)
	for k, v := range b {
		if sub, ok := v.(map[string]any); ok {
			if base, ok := out[k].(map[string]any); ok {
				out[k] = deepMerge(base, sub)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

// animateElements attaches template keyframes to standard layout elements.
func (c *advancedContext) animateElements(animate obj) error {
	for si := range c.plan.Scenes {
		scene := &c.plan.Scenes[si]
		specs := object(animate[scene.Role])
		if len(specs) == 0 {
			continue
		}
		for li := range scene.Layers {
			layer := &scene.Layers[li]
			spec := object(specs[layer.Element])
			if layer.Element == "" || len(spec) == 0 {
				continue
			}
			frames, err := c.keyframeList(spec["keyframes"])
			if err != nil {
				return fmt.Errorf("animate.%s.%s: %w", scene.Role, layer.Element, err)
			}
			anim, err := parseKeyframes(frames, scene.Duration, textProps)
			if err == nil {
				err = checkReveal(*layer, anim)
			}
			if err != nil {
				return fmt.Errorf("animate.%s.%s: %w", scene.Role, layer.Element, err)
			}
			if anim == nil {
				continue
			}
			if layer.Kind == "text" && hasEffect(layer.Effects, "glass_panel") {
				return fmt.Errorf("animate.%s.%s: animated text cannot use glass_panel", scene.Role, layer.Element)
			}
			layer.Animation = anim
		}
	}
	return nil
}

// keyframeList resolves a preset name or an inline list whose items may
// splice presets with {use: name}.
func (c *advancedContext) keyframeList(v any) ([]any, error) {
	return c.resolvePresets(v, c.keyframes, "keyframe")
}

func (c *advancedContext) resolvePresets(v any, presets obj, kind string) ([]any, error) {
	if name, ok := v.(string); ok {
		preset, ok := presets[name]
		if !ok {
			return nil, fmt.Errorf("unknown %s preset %q", kind, name)
		}
		return c.resolvePresets(preset, obj{}, kind)
	}
	var out []any
	for _, raw := range list(v) {
		item := object(raw)
		if use, ok := item["use"]; ok {
			spliced, err := c.resolvePresets(use, presets, kind)
			if err != nil {
				return nil, err
			}
			out = append(out, spliced...)
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}

// owner is the timeline a track is attached to: one scene or the whole video,
// or a folder within one of them.
type owner struct {
	scene    *ScenePlan // nil for the video
	index    int
	length   time.Duration
	bindings map[string]string
	// offset is where this timeline starts on the scene's (or video's): the
	// folder's start for a folder's children. Audio is placed with it.
	offset time.Duration
	// variant marks a rebuilt element variant, whose audio is already mixed.
	variant bool
}

// owners resolves a track's attach value: the video, a role, a layout name,
// or a scene id.
func (c *advancedContext) owners(attach string) ([]owner, error) {
	common := map[string]string{"duration": num6(c.plan.Duration.Seconds())}
	for k, v := range object(c.video["metadata"]) {
		if s, ok := v.(string); ok {
			common["metadata."+k] = s
		}
	}
	if attach == "video" {
		return []owner{{index: -1, length: c.plan.Duration, bindings: common}}, nil
	}

	role := attach == "intro" || attach == "slide" || attach == "outro"
	var result []owner
	for i := range c.plan.Scenes {
		scene := &c.plan.Scenes[i]
		if attach != scene.Role && attach != scene.ID && (role || attach != scene.Layout) {
			continue
		}
		bindings := merge2(common, scene.bindings)
		result = append(result, owner{scene: scene, index: i, length: scene.Duration, bindings: bindings})
	}
	if len(result) == 0 && !role && !c.isLayout(attach) {
		return nil, fmt.Errorf("attach %q matches no scene or layout", attach)
	}
	return result, nil
}

func (c *advancedContext) isLayout(name string) bool {
	_, ok := object(c.template["layout"])[name]
	return ok
}

func merge2(a, b map[string]string) map[string]string {
	out := map[string]string{}
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}

// missingBinding reports a $(NAME) without a value or fallback.
type missingBinding struct{ name string }

func (e missingBinding) Error() string { return fmt.Sprintf("unknown variable $(%s)", e.name) }

// bind substitutes $(NAME), ${NAME}, and their |fallback forms.
func bind(value string, bindings map[string]string) (string, error) {
	var missing error
	out := bindingRe.ReplaceAllStringFunc(value, func(m string) string {
		if m == "$$" {
			return "$"
		}
		g := bindingRe.FindStringSubmatch(m)
		name, fallback, hasFallback := g[1], g[2], strings.Contains(m, "|")
		if name == "" {
			name, fallback = g[3], g[4]
		}
		if v, ok := bindings[name]; ok {
			return v
		}
		if !hasFallback && missing == nil {
			missing = missingBinding{name}
		}
		return fallback
	})
	return out, missing
}

func (c *advancedContext) addTrack(track obj, trackIndex int) error {
	owners, err := c.owners(str(choose(track["attach"], "video")))
	if err != nil {
		return err
	}
	targeted := map[int]bool{}
	for _, o := range owners {
		// The offset delays the whole track; its clips are timed within the
		// window from the offset to the owner's end.
		shift, err := resolvePoint(choose(track["offset"], "0s"), o.length)
		if err != nil || shift >= o.length {
			return fmt.Errorf("offset %v must start before the end of its owner (%s)", track["offset"], o.length)
		}
		window := o
		window.length -= shift

		plan := TrackPlan{ID: str(track["id"]), ASSPrefix: fmt.Sprintf("video-track-%02d", trackIndex)}
		if o.scene != nil {
			plan.ASSPrefix = fmt.Sprintf("scene-%04d-track-%02d", o.index, trackIndex)
		}
		for ci, raw := range list(track["clips"]) {
			clip := object(raw)
			if str(clip["type"]) == "element" {
				hit, err := c.targetElement(clip, window, shift)
				if err != nil {
					return fmt.Errorf("clip %d (element %q): %w", ci, clip["target"], err)
				}
				targeted[ci] = targeted[ci] || hit
				continue
			}
			if err := c.addClip(&plan, clip, window, shift); err != nil {
				return fmt.Errorf("clip %d (%s): %w", ci, clip["type"], err)
			}
		}
		if len(plan.Clips) == 0 {
			continue
		}
		if err := attachToText(plan.Clips, c.plan.Video); err != nil {
			return fmt.Errorf("clip %w", err)
		}
		if o.scene == nil {
			c.plan.Tracks = append(c.plan.Tracks, plan)
		} else {
			o.scene.Tracks = append(o.scene.Tracks, plan)
		}
	}
	for ci, raw := range list(track["clips"]) {
		if clip := object(raw); str(clip["type"]) == "element" && !targeted[ci] && len(owners) > 0 {
			return fmt.Errorf("clip %d: target %q matches no element in the attached scenes", ci, clip["target"])
		}
	}
	return nil
}

// clipTiming resolves a clip's start and length within its window o, which
// begins shift after the owner's start; the start returned is owner time.
func clipTiming(clip obj, o owner, shift time.Duration) (time.Duration, time.Duration, error) {
	start, err := resolvePoint(choose(clip["start"], "0s"), o.length)
	if err != nil {
		return 0, 0, fmt.Errorf("start: %w", err)
	}
	length := o.length - start
	if clip["duration"] != nil {
		if length, err = duration(clip["duration"]); err != nil {
			return 0, 0, err
		}
	}
	if length <= 0 || start+length > o.length {
		return 0, 0, fmt.Errorf("clip %s–%s exceeds its owner (%s)", shift+start, shift+start+length, o.length+shift)
	}
	return start + shift, length, nil
}

// addClip compiles one clip onto a track (or, for audio, into its owner's mix).
func (c *advancedContext) addClip(track *TrackPlan, clip obj, o owner, shift time.Duration) error {
	start, length, err := clipTiming(clip, o, shift)
	if err != nil {
		return err
	}
	clips, err := c.buildClip(clip, o, start, length)
	var missing missingBinding
	if errors.As(err, &missing) && clip["optional"] == true {
		return nil
	}
	if err != nil {
		return err
	}
	track.Clips = append(track.Clips, clips...)
	return nil
}

// buildClip compiles a clip definition placed at start for length on owner o.
// Audio clips go straight into the owner's mix and return no visual clips.
func (c *advancedContext) buildClip(clip obj, o owner, start, length time.Duration) ([]ClipPlan, error) {
	kind := str(clip["type"])
	allowed := visualProps
	switch kind {
	case "audio":
		allowed = audioProps
	case "effect":
		allowed = effectProps
	case "text":
		allowed = textProps
	case "image", "rect", "circle":
		allowed = sizedProps
	}
	frames, err := c.keyframeList(clip["keyframes"])
	if err != nil {
		return nil, err
	}
	anim, err := parseKeyframes(frames, length, allowed)
	if err != nil {
		return nil, err
	}

	video := c.plan.Video
	plan := ClipPlan{ID: str(clip["id"]), Kind: kind, Start: start, Duration: length, def: clip, bindings: o.bindings}
	switch kind {
	case "image":
		value, err := bind(str(clip["path"]), o.bindings)
		if err != nil {
			return nil, err
		}
		source, err := mediaPath(c.base, value, "image")
		if err != nil {
			return nil, err
		}
		layer, err := mediaLayer(source, clip, c.media, video)
		if err != nil {
			return nil, err
		}
		layer.Opacity = num(choose(clip["opacity"], jsonNumber(1)))
		layer.Duration = length
		layer.Animation = anim
		if err := validateEffects(layer, length); err != nil {
			return nil, err
		}
		fitAnimatedSize(&layer)
		plan.Layer = layer
	case "rect", "circle":
		w, h := integer(clip["width"]), integer(clip["height"])
		if attached := clip["cover"] != nil || clip["caret"] != nil; attached {
			// Sized and placed on their text later (attachToText).
			w, h = max(w, 1), max(h, 1)
		}
		if w < 1 || h < 1 {
			return nil, fmt.Errorf("width and height must be positive")
		}
		color, err := bind(str(clip["color"]), o.bindings)
		if err != nil {
			return nil, err
		}
		if !v2ASSColorSupported(color) {
			return nil, fmt.Errorf("color %q must be #RRGGBB", color)
		}
		x, y := bounds(clip, video, w, h)
		shape := "rectangle"
		if kind == "circle" {
			shape = "circle"
		} else if clip["corner_radius"] != nil {
			shape = "rounded-rectangle"
		}
		plan.Layer = LayerPlan{Kind: kind, X: x, Y: y, Width: w, Height: h, Color: color, Shape: shape,
			CornerRadius: integer(clip["corner_radius"]), Opacity: num(choose(clip["opacity"], jsonNumber(1))), Duration: length, Animation: anim}
		fitAnimatedSize(&plan.Layer)
	case "text":
		value, err := bind(str(clip["text"]), o.bindings)
		if err != nil {
			return nil, err
		}
		if r := object(clip["replace"]); len(r) > 0 {
			re, err := bindPattern(r["pattern"], o.bindings)
			if err != nil {
				return nil, fmt.Errorf("replace: %w", err)
			}
			with, err := bind(str(r["with"]), o.bindings)
			if err != nil {
				return nil, fmt.Errorf("replace: %w", err)
			}
			value = re.ReplaceAllString(value, with)
		}
		layer, err := textLayer(value, "text", clip, c.typography, c.theme, video)
		if err != nil {
			return nil, err
		}
		if clip["match"] != nil {
			if layer.Match, err = bindPattern(clip["match"], o.bindings); err != nil {
				return nil, fmt.Errorf("match: %w", err)
			}
		}
		layer.Align = str(object(clip["style"])["align"])
		layer.Duration = length
		layer.Animation = anim
		if r := object(clip["reveal"]); len(r) > 0 {
			reveal := defaultReveal
			for name, dst := range map[string]*RevealValue{"chars_start": &reveal.CharsStart, "chars_end": &reveal.CharsEnd,
				"lines_start": &reveal.LinesStart, "lines_end": &reveal.LinesEnd} {
				if r[name] == nil {
					continue
				}
				if dst.Value, dst.Relative, err = revealValue(r[name]); err != nil {
					return nil, fmt.Errorf("reveal.%s: %w", name, err)
				}
			}
			layer.Reveal = &reveal
		}
		if anim != nil && hasEffect(layer.Effects, "glass_panel") {
			return nil, fmt.Errorf("animated text cannot use glass_panel")
		}
		if err := validateEffects(layer, length); err != nil {
			return nil, err
		}
		plan.Layer = layer
	case "progress":
		if o.scene == nil || o.scene.Role != "slide" {
			return nil, fmt.Errorf("progress needs a slide")
		}
		if anim != nil {
			return nil, fmt.Errorf("progress cannot be animated")
		}
		var bars ScenePlan
		if err := addProgress(&bars, clip, o.scene.index+1, o.scene.total, c.theme, video); err != nil {
			return nil, err
		}
		var out []ClipPlan
		for _, bar := range bars.Layers {
			box := plan
			box.Kind, box.Layer = "box", bar
			out = append(out, box)
		}
		return out, nil
	case "audio":
		value, err := bind(str(clip["path"]), o.bindings)
		if err != nil {
			return nil, err
		}
		a, err := audioLayer(merge(clip, obj{"path": value}), c.audio, c.base, o.offset+start, length)
		if err != nil {
			return nil, err
		}
		a.SoundEffect = clip["ducks"] != true
		a.VolumeAnimation = anim
		if o.variant {
			return nil, nil
		}
		if o.scene == nil {
			c.plan.Audio = append(c.plan.Audio, a)
		} else {
			o.scene.Audio = append(o.scene.Audio, a)
		}
		return nil, nil
	case "effect":
		effect, err := c.effectClip(clip, length)
		if err != nil {
			return nil, err
		}
		effect.ID, effect.Start, effect.def, effect.bindings = plan.ID, start, clip, o.bindings
		plan = effect
	case "folder":
		folder, err := c.buildFolder(clip, o, start, length)
		if err != nil {
			return nil, err
		}
		folder.Layer.Animation = anim
		plan.Layer, plan.Folder = folder.Layer, folder.Folder
	case "element":
		return nil, fmt.Errorf("element clips target layout elements and belong in advanced tracks")
	default:
		return nil, fmt.Errorf("unsupported clip type %q", kind)
	}
	return []ClipPlan{plan}, nil
}

// bindPattern compiles a regular expression after filling its $(VARIABLES).
func bindPattern(v any, bindings map[string]string) (*regexp.Regexp, error) {
	pattern, err := bind(str(v), bindings)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern %q: %w", pattern, err)
	}
	return re, nil
}

// buildFolder compiles a folder's children on a timeline that starts with the
// folder, in list order like a track; folders nest. The folder's effects run
// over its composited children, and its keyframes (set by the caller) move
// the whole group around its pivot: the children's center unless set.
func (c *advancedContext) buildFolder(clip obj, o owner, start, length time.Duration) (ClipPlan, error) {
	c.folders++
	children := TrackPlan{ID: "folder", ASSPrefix: fmt.Sprintf("folder-%03d", c.folders)}
	inner := owner{scene: o.scene, index: o.index, length: length, bindings: o.bindings, offset: o.offset + start, variant: o.variant}
	for i, raw := range list(clip["clips"]) {
		child := object(raw)
		if str(child["type"]) == "element" {
			return ClipPlan{}, fmt.Errorf("folder clip %d: element clips belong in tracks", i)
		}
		if err := c.addClip(&children, child, inner, 0); err != nil {
			label := fmt.Sprintf("folder clip %d (%s)", i, child["type"])
			if id := str(child["id"]); id != "" {
				label = fmt.Sprintf("folder clip %q", id)
			}
			return ClipPlan{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	if err := attachToText(children.Clips, c.plan.Video); err != nil {
		return ClipPlan{}, fmt.Errorf("folder clip %w", err)
	}
	if clip["effects"] != nil {
		effect, err := c.effectClip(obj{"type": "effect", "effects": clip["effects"]}, length)
		if err != nil {
			return ClipPlan{}, fmt.Errorf("folder effects: %w", err)
		}
		children.Clips = append(children.Clips, effect)
	}

	video := c.plan.Video
	pivot := [2]float64{float64(video.Width) / 2, float64(video.Height) / 2}
	if clip["pivot"] != nil {
		x, y := bounds(obj{"placement": clip["pivot"]}, video, 0, 0)
		pivot = [2]float64{float64(x), float64(y)}
	} else if box, ok := clipsBounds(children.Clips); ok {
		pivot = [2]float64{float64(box[0]+box[2]) / 2, float64(box[1]+box[3]) / 2}
	}
	canvas := folderCanvas(children.Clips, video)
	children.Canvas = &canvas
	layer := LayerPlan{Kind: "folder", X: canvas.X, Y: canvas.Y, Width: canvas.W, Height: canvas.H, Duration: length, Pivot: &pivot,
		Opacity: num(choose(clip["opacity"], jsonNumber(1)))}
	return ClipPlan{Kind: "folder", Layer: layer, Folder: &children}, nil
}

// canvasMargin pads folder canvases for glows, blurs, outlines, and shakes.
const canvasMargin = 32

// folderCanvas is the area a folder's children can reach: their boxes grown
// by their own keyframes, plus a margin, in frame coordinates and possibly
// beyond the frame (within one frame size on each side). It is the frame
// when nothing has a box.
func folderCanvas(clips []ClipPlan, video VideoSpec) Rect {
	var box [4]float64
	found := false
	for _, clip := range clips {
		b, ok := clipExtent(clip)
		if !ok {
			continue
		}
		if !found {
			box, found = b, true
			continue
		}
		box = [4]float64{math.Min(box[0], b[0]), math.Min(box[1], b[1]), math.Max(box[2], b[2]), math.Max(box[3], b[3])}
	}
	if !found {
		return Rect{0, 0, video.Width, video.Height}
	}
	w, h := float64(video.Width), float64(video.Height)
	left := math.Max(math.Floor(box[0])-canvasMargin, -w)
	top := math.Max(math.Floor(box[1])-canvasMargin, -h)
	right := math.Min(math.Ceil(box[2])+canvasMargin, 2*w)
	bottom := math.Min(math.Ceil(box[3])+canvasMargin, 2*h)
	// Even sizes keep chroma-subsampled steps exact.
	cw, ch := int(right-left+1)&^1, int(bottom-top+1)&^1
	return Rect{int(left), int(top), max(cw, 2), max(ch, 2)}
}

// fitAnimatedSize lays out a layer with width or height keyframes at the
// largest size they reach, centered on its rest box (overshooting easings
// included). The render masks it to the animated size frame by frame.
func fitAnimatedSize(l *LayerPlan) {
	if !l.Animation.Has("width", "height") {
		return
	}
	grow := func(name string, size int) int {
		frames := l.Animation.Props[name]
		if len(frames) == 0 {
			return size
		}
		lo, hi, over := frames[0].Value, frames[0].Value, 0.0
		for _, f := range frames {
			lo, hi = math.Min(lo, f.Value), math.Max(hi, f.Value)
			switch f.Ease {
			case "back-out":
				over = math.Max(over, 0.1)
			case "elastic-out":
				over = math.Max(over, 0.4)
			}
		}
		grown := max(size, int(math.Ceil(hi+(hi-lo)*over)))
		// An even growth keeps the rest center on a whole pixel.
		return size + (grown-size+1)&^1
	}
	w, h := grow("width", l.Width), grow("height", l.Height)
	l.X -= (w - l.Width) / 2
	l.Y -= (h - l.Height) / 2
	l.Width, l.Height = w, h
}

// clipExtent is the frame area a clip can cover over its keyframes.
func clipExtent(clip ClipPlan) ([4]float64, bool) {
	l := clip.Layer
	var box [4]float64
	switch clip.Kind {
	case "folder":
		c := clip.Folder.Canvas
		box = [4]float64{float64(c.X), float64(c.Y), float64(c.X + c.W), float64(c.Y + c.H)}
	case "text":
		// The measured lines, plus room for glyphs that reach past their
		// advances and line boxes (italics, swashes, accents).
		g := layerGeometry(l, VideoSpec{})
		box = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for _, line := range g.lines {
			box = [4]float64{math.Min(box[0], line.X0), math.Min(box[1], line.Top), math.Max(box[2], line.X1), math.Max(box[3], line.Top+g.size)}
		}
		over := g.size / 4
		box = [4]float64{box[0] - over, box[1] - over, box[2] + over, box[3] + over}
	case "image", "rect", "circle", "box":
		box = [4]float64{float64(l.X), float64(l.Y), float64(l.X + l.Width), float64(l.Y + l.Height)}
	default:
		return box, false
	}
	return growByAnimation(box, l.Animation), true
}

// growByAnimation widens a box by the range of its x/y keyframes, its largest
// scale, and its rotation, with headroom for overshooting easings.
func growByAnimation(box [4]float64, anim *Animation) [4]float64 {
	if anim == nil {
		return box
	}
	span := func(name string) (lo, hi float64) {
		for _, f := range anim.Props[name] {
			lo, hi = math.Min(lo, f.Value), math.Max(hi, f.Value)
		}
		over := (hi - lo) * 0.15
		return lo - over, hi + over
	}
	x0, x1 := span("x")
	y0, y1 := span("y")
	box = [4]float64{box[0] + x0, box[1] + y0, box[2] + x1, box[3] + y1}

	peak := func(name string) float64 {
		m := 1.0
		for _, f := range anim.Props[name] {
			m = math.Max(m, f.Value)
		}
		return m
	}
	cx, cy := (box[0]+box[2])/2, (box[1]+box[3])/2
	hw, hh := (box[2]-box[0])/2, (box[3]-box[1])/2
	if anim.Has("scale", "scale_x", "scale_y") {
		s := peak("scale") * 1.1
		hw, hh = hw*s*peak("scale_x"), hh*s*peak("scale_y")
	}
	if anim.Has("rotation") {
		r := math.Hypot(hw, hh)
		hw, hh = r, r
	}
	return [4]float64{cx - hw, cy - hh, cx + hw, cy + hh}
}

// clipsBounds returns the frame box (left, top, right, bottom) covering the
// visual clips at their rest positions, through nested folders.
func clipsBounds(clips []ClipPlan) ([4]int, bool) {
	var box [4]int
	found := false
	grow := func(b [4]int) {
		if !found {
			box, found = b, true
			return
		}
		box = [4]int{min(box[0], b[0]), min(box[1], b[1]), max(box[2], b[2]), max(box[3], b[3])}
	}
	for _, clip := range clips {
		switch clip.Kind {
		case "folder":
			if b, ok := clipsBounds(clip.Folder.Clips); ok {
				grow(b)
			}
		case "image", "rect", "circle", "text", "box":
			l := clip.Layer
			grow([4]int{l.X, l.Y, l.X + l.Width, l.Y + l.Height})
		}
	}
	return box, found
}

// checkReveal rejects reveal keyframes on anything but text.
func checkReveal(layer LayerPlan, anim *Animation) error {
	if anim.Has(revealProps...) && layer.Kind != "text" {
		return fmt.Errorf("reveal keyframes (%s) need a text element", strings.Join(revealProps, ", "))
	}
	return nil
}

// targetElement applies an element clip to the layout element it targets in
// o's scene: keyframes animate the element, and set modifies it during the
// clip by showing a recompiled variant in its place.
func (c *advancedContext) targetElement(clip obj, o owner, shift time.Duration) (bool, error) {
	if o.scene == nil {
		return false, fmt.Errorf("element clips need a scene; attach the track to a role, layout, or scene")
	}
	start, length, err := clipTiming(clip, o, shift)
	if err != nil {
		return false, err
	}
	frames, err := c.keyframeList(clip["keyframes"])
	if err != nil {
		return false, err
	}
	anim, err := parseKeyframes(frames, length, textProps)
	if err != nil {
		return false, err
	}
	set := object(clip["set"])
	if anim == nil && len(set) == 0 {
		return false, fmt.Errorf("element clips need keyframes or set")
	}
	hide := set["visible"] == false
	if v, ok := set["visible"]; ok && v != false && v != true {
		return false, fmt.Errorf("set.visible must be true or false")
	}
	set = merge(set, nil)
	delete(set, "visible")
	target := str(clip["target"])
	window := Window{start, start + length}
	scene := o.scene
	hit := false

	// Classic layout elements: their timeline starts with the scene.
	for i := 0; i < len(scene.Layers); i++ {
		layer := &scene.Layers[i]
		if layer.Element != target {
			continue
		}
		hit = true
		if err := checkReveal(*layer, anim); err != nil {
			return true, err
		}
		if len(set) == 0 && !hide {
			layer.Animation = overrideAnimation(layer.Animation, anim, start)
			if err := checkAnimatedPanel(*layer); err != nil {
				return true, err
			}
			continue
		}
		shown, overlaps := variantWindow(*layer, window)
		if !overlaps {
			continue
		}
		layer.HideDuring = append(layer.HideDuring, shown)
		if hide {
			continue
		}
		variant, err := c.classicVariant(*scene, *layer, set)
		if err != nil {
			return true, err
		}
		variant.ShowOnly = &shown
		variant.HideDuring = nil
		variant.Animation = overrideAnimation(layer.Animation, anim, start)
		if err := checkAnimatedPanel(variant); err != nil {
			return true, err
		}
		scene.Layers = append(scene.Layers[:i+1], append([]LayerPlan{variant}, scene.Layers[i+1:]...)...)
		i++
	}

	// Custom layout elements, also inside folders.
	edit := elementEdit{target: target, anim: anim, set: set, hide: hide, window: window, start: start}
	for ti := range scene.Tracks {
		track := &scene.Tracks[ti]
		if track.ID != "layout" {
			continue
		}
		found, err := c.targetClips(&track.Clips, edit, owner{scene: scene, index: o.index, length: scene.Duration})
		hit = hit || found
		if err != nil {
			return true, err
		}
	}
	return hit, nil
}

// elementEdit is one element clip, timed on its scene's timeline.
type elementEdit struct {
	target string
	anim   *Animation
	set    obj
	hide   bool
	window Window
	start  time.Duration
}

// targetClips applies e to the clips with its target id in one timeline: a
// scene's layout, or a folder's children (o.offset is where that timeline
// starts in the scene). A clip's timeline starts with the clip itself, and
// its show/hide windows are on the timeline it lives in.
func (c *advancedContext) targetClips(clips *[]ClipPlan, e elementEdit, o owner) (bool, error) {
	hit := false
	for i := 0; i < len(*clips); i++ {
		element := &(*clips)[i]
		if element.Folder != nil {
			inner := owner{scene: o.scene, index: o.index, length: element.Duration, offset: o.offset + element.Start}
			found, err := c.targetClips(&element.Folder.Clips, e, inner)
			hit = hit || found
			if err != nil {
				return true, err
			}
		}
		if element.ID != e.target {
			continue
		}
		hit = true
		if element.Kind == "box" {
			return true, fmt.Errorf("progress elements cannot be animated or modified")
		}
		if err := checkReveal(element.Layer, e.anim); err != nil {
			return true, err
		}
		animStart := e.start - o.offset - element.Start
		if len(e.set) == 0 && !e.hide {
			element.Layer.Animation = overrideAnimation(element.Layer.Animation, e.anim, animStart)
			if err := checkAnimatedPanel(element.Layer); err != nil {
				return true, err
			}
			continue
		}
		local := Window{e.window.Start - o.offset, e.window.End - o.offset}
		shown, overlaps := variantWindow(element.Layer, local)
		if !overlaps {
			continue
		}
		element.Layer.HideDuring = append(element.Layer.HideDuring, shown)
		if e.hide {
			continue
		}
		modified := deepMerge(element.def, e.set)
		if err := validateDefinition(modified, "element"); err != nil {
			return true, err
		}
		rebuild := owner{scene: o.scene, index: o.index, length: o.length, bindings: element.bindings, offset: o.offset, variant: true}
		variants, err := c.buildClip(modified, rebuild, element.Start, element.Duration)
		if err != nil {
			return true, err
		}
		for v := range variants {
			if element.def["cover"] != nil || element.def["caret"] != nil {
				// Attached to a text: placed like the original.
				l := element.Layer
				variants[v].Layer.X, variants[v].Layer.Y, variants[v].Layer.Width, variants[v].Layer.Height = l.X, l.Y, l.Width, l.Height
			}
			variants[v].Layer.ShowOnly = &shown
			variants[v].Layer.HideDuring = nil
			variants[v].Layer.Animation = overrideAnimation(element.Layer.Animation, e.anim, animStart)
			if err := checkAnimatedPanel(variants[v].Layer); err != nil {
				return true, err
			}
		}
		*clips = append((*clips)[:i+1], append(variants, (*clips)[i+1:]...)...)
		i += len(variants)
	}
	return hit, nil
}

// variantWindow is when a set-clip variant of layer shows: the clip's window,
// narrowed to the layer's own window when the layer is itself a variant from an
// earlier clip. It reports false when they don't overlap; the layer is then left
// alone, so clips with separate windows don't multiply each other's variants.
func variantWindow(layer LayerPlan, window Window) (Window, bool) {
	if layer.ShowOnly == nil {
		return window, true
	}
	shown := Window{max(window.Start, layer.ShowOnly.Start), min(window.End, layer.ShowOnly.End)}
	return shown, shown.Start < shown.End
}

func checkAnimatedPanel(layer LayerPlan) error {
	if layer.Kind == "text" && layer.Animation != nil && hasEffect(layer.Effects, "glass_panel") {
		return fmt.Errorf("animated text cannot use glass_panel")
	}
	return nil
}

// classicVariant recompiles a classic layout element with set applied: text
// and path replace its content, other keys merge into its layout definition.
func (c *advancedContext) classicVariant(scene ScenePlan, layer LayerPlan, set obj) (LayerPlan, error) {
	content := layer.source
	key := "text"
	if layer.Kind != "text" {
		key = "path"
	}
	if v, ok := set[key]; ok {
		bound, err := bind(str(v), scene.bindings)
		if err != nil {
			return LayerPlan{}, err
		}
		content = bound
		if key == "path" {
			if content, err = mediaPath(c.base, bound, "image"); err != nil {
				return LayerPlan{}, err
			}
		}
	}
	el := deepMerge(layer.def, set)
	delete(el, key)
	if layer.Kind != "text" {
		delete(el, "opacity")
	}
	if layer.def != nil {
		def := "media_element"
		if layer.Kind == "text" {
			def = "text_element"
		}
		if err := validateDefinition(el, def); err != nil {
			return LayerPlan{}, err
		}
	}

	var variant LayerPlan
	var err error
	switch {
	case layer.Kind == "text":
		variant, err = textLayer(content, layer.Element, el, c.typography, c.theme, c.plan.Video)
		variant.Align = str(object(el["style"])["align"])
	case layer.def == nil: // intro/outro full-frame background image
		variant = layer
		variant.Path = content
	default:
		variant, err = mediaLayer(content, el, c.media, c.plan.Video)
		variant.AltText = layer.AltText
	}
	if err != nil {
		return LayerPlan{}, err
	}
	variant.Element, variant.def, variant.source = layer.Element, el, content
	if variant.Kind != "text" {
		variant.Opacity = num(choose(set["opacity"], jsonNumber(layer.Opacity)))
	}
	if variant.Opacity < 0 || variant.Opacity > 1 || math.IsNaN(variant.Opacity) {
		return LayerPlan{}, fmt.Errorf("opacity must be between 0 and 1")
	}
	return variant, validateEffects(variant, scene.Duration)
}

// overrideAnimation returns base with the properties animated by add replaced
// by add's keyframes, shifted by offset into base's timeline.
func overrideAnimation(base, add *Animation, offset time.Duration) *Animation {
	if add == nil {
		return base
	}
	out := &Animation{Props: map[string][]Keyframe{}}
	if base != nil {
		maps.Copy(out.Props, base.Props)
	}
	for name, frames := range add.Props {
		shifted := make([]Keyframe, len(frames))
		for i, f := range frames {
			f.At += offset
			shifted[i] = f
		}
		out.Props[name] = shifted
	}
	return out
}

// effectClip builds an effect layer of the given length from effects (a stack
// or preset name), keyframes (mix opacity and camera), and opacity.
func (c *advancedContext) effectClip(spec obj, length time.Duration) (ClipPlan, error) {
	frames, err := c.keyframeList(spec["keyframes"])
	if err != nil {
		return ClipPlan{}, err
	}
	anim, err := parseKeyframes(frames, length, effectProps)
	if err != nil {
		return ClipPlan{}, err
	}
	stack, err := c.resolvePresets(spec["effects"], c.effects, "effect")
	if err != nil {
		return ClipPlan{}, err
	}
	if len(stack) == 0 && !anim.Has(cameraProps...) {
		return ClipPlan{}, fmt.Errorf("effect layer needs effects or camera keyframes")
	}
	clip := ClipPlan{Kind: "effect", Duration: length, Mix: num(choose(spec["opacity"], jsonNumber(1))),
		Layer: LayerPlan{Kind: "effect", Width: c.plan.Video.Width, Height: c.plan.Video.Height, Animation: anim}}
	for _, raw := range stack {
		adj, err := adjustment(object(raw))
		if err != nil {
			return ClipPlan{}, err
		}
		clip.Adjustments = append(clip.Adjustments, adj)
	}
	return clip, nil
}

var adjustmentTypes = map[string]bool{
	"blur": true, "color": true, "color_filter": true, "vignette": true, "glow": true,
	"rgb_split": true, "grain": true, "tint": true, "shake": true,
}

func adjustment(v obj) (AdjustmentPlan, error) {
	typ := str(v["type"])
	if !adjustmentTypes[typ] {
		return AdjustmentPlan{}, fmt.Errorf("unsupported effect %q", typ)
	}
	a := AdjustmentPlan{
		Type: typ, Radius: num(choose(v["radius"], jsonNumber(8))), Strength: num(choose(v["strength"], jsonNumber(0.6))),
		Amount: num(choose(v["amount"], jsonNumber(6))), Brightness: num(v["brightness"]), Contrast: num(choose(v["contrast"], jsonNumber(1))),
		Saturation: num(choose(v["saturation"], jsonNumber(1))), Gamma: num(choose(v["gamma"], jsonNumber(1))), Hue: num(v["hue"]),
		Frequency: num(choose(v["frequency"], jsonNumber(12))), Color: str(choose(v["color"], "#FFFFFF")),
		Opacity: num(choose(v["opacity"], jsonNumber(0.5))), Preset: str(v["preset"]), Intensity: num(choose(v["intensity"], jsonNumber(1))),
	}
	if typ == "color_filter" {
		switch a.Preset {
		case "grayscale", "sepia", "warm", "cool", "high-contrast":
		default:
			return a, fmt.Errorf("unsupported color preset %q", a.Preset)
		}
	}
	if strings.HasPrefix(a.Color, "#") && len(a.Color) != 7 {
		return a, fmt.Errorf("effect color must be #RRGGBB")
	}
	return a, nil
}
