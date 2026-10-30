package render

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/ondics/yaml2video/docs"
)

// Templates define layouts by name. intro, slide, and outro are the defaults
// for their roles; every other key is a named layout a section or content item
// can choose with `layout:`. A layout is either classic (the fixed elements of
// its role, e.g. title/image/text/tip), custom (an ordered list of elements:
// text, image, rect, circle, progress, audio, effect), or the name of another
// layout.

var layoutNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// resolveLayout picks a scene's layout: the item's, else the section's, else
// the role default. It follows aliases and checks classic layouts fit the role.
func resolveLayout(layouts obj, role string, section, item obj) (string, any, error) {
	name := str(choose(item["layout"], section["layout"], role))
	seen := map[string]bool{}
	for {
		def, ok := layouts[name]
		if !ok {
			return "", nil, fmt.Errorf("unknown layout %q", name)
		}
		alias, isAlias := def.(string)
		if !isAlias {
			if !isCustomLayout(def) {
				kind := name
				if kind != "intro" && kind != "outro" {
					kind = "slide"
				}
				if kind != role {
					return "", nil, fmt.Errorf("layout %q is a classic %s layout and cannot be used for the %s", name, kind, role)
				}
			}
			return name, def, nil
		}
		if seen[name] {
			return "", nil, fmt.Errorf("layout alias loop at %q", name)
		}
		seen[name] = true
		name = alias
	}
}

func isCustomLayout(def any) bool {
	if _, ok := def.([]any); ok {
		return true
	}
	_, ok := object(def)["elements"]
	return ok
}

// customLayout returns a custom layout's elements and background.
func customLayout(def any, theme obj) ([]any, any) {
	if elements, ok := def.([]any); ok {
		return elements, theme["background"]
	}
	l := object(def)
	return list(l["elements"]), choose(l["background"], theme["background"])
}

func backgroundColor(background any) string {
	if color, ok := background.(string); ok {
		return color
	}
	return str(choose(object(background)["color"], "black"))
}

// Image backgrounds are ordinary full-frame media beneath the layout's layers.
func composeBackground(scene *ScenePlan, background any, p *Plan, base string, media obj) error {
	scene.Background = backgroundColor(background)
	image := object(background)
	if len(image) == 0 {
		return nil
	}
	value, err := bind(str(image["path"]), scene.bindings)
	if err != nil {
		return err
	}
	source, err := mediaPath(base, value, "image")
	if err != nil {
		return err
	}
	layer, err := mediaLayer(source, image, media, p.Video)
	if err != nil {
		return err
	}
	scene.Layers = append(scene.Layers, layer)
	return nil
}

func validateLayoutNames(layouts obj) error {
	for name := range layouts {
		if !layoutNameRe.MatchString(name) {
			return fmt.Errorf("layout name %q must match %s", name, layoutNameRe)
		}
	}
	return nil
}

// sceneBindings collects the values $(NAME) can read for one scene: its item's
// variables first, then the item's own text fields, then generated values.
func sceneBindings(item obj, sceneID, role, layout string, index, total int, logo string, meta obj) map[string]string {
	b := map[string]string{"scene": sceneID, "role": role, "layout": layout}
	for k, v := range meta {
		if s, ok := v.(string); ok {
			b["metadata."+k] = s
		}
	}
	if logo != "" {
		b["logo"] = logo
	}
	if role == "slide" {
		b["index"] = fmt.Sprint(index + 1)
		b["total"] = fmt.Sprint(total)
	}
	for k, v := range item {
		if s, ok := v.(string); ok {
			b[k] = s
		}
	}
	for k, v := range object(item["variables"]) {
		switch value := v.(type) {
		case string:
			b[k] = value
		case json.Number:
			b[k] = value.String()
		case bool:
			b[k] = fmt.Sprint(value)
		}
	}
	return b
}

// composeClassicScene renders a classic layout: the role's fixed elements,
// filled from the content item's fields.
func composeClassicScene(scene *ScenePlan, l, item obj, p *Plan, base string, defaults, assets obj, logo string) error {
	name, role := scene.ID, scene.Role
	switch {
	case role == "slide" && (item["image"] == nil || item["text"] == nil):
		return fmt.Errorf("%s: layout %q needs the slide's image and text", name, scene.Layout)
	case role == "intro" && item["title"] == nil && item["image"] == nil:
		return fmt.Errorf("%s: layout %q needs a title or an image", name, scene.Layout)
	case role == "outro" && item["cta"] == nil:
		return fmt.Errorf("%s: layout %q needs a cta", name, scene.Layout)
	}
	media, typography, theme := object(defaults["media"]), object(defaults["typography"]), object(defaults["theme"])
	if err := composeBackground(scene, choose(l["background"], theme["background"]), p, base, media); err != nil {
		return fmt.Errorf("%s.background: %w", name, err)
	}

	addMedia := func(key string, asset any) error {
		if asset == nil {
			return nil
		}
		el := object(l[key])
		if len(el) == 0 {
			return fmt.Errorf("%s: %s has content but no layout", name, key)
		}
		if el["visible"] == false {
			return nil
		}
		source, err := mediaPath(base, asset, "image")
		if err != nil {
			return err
		}
		layer, err := mediaLayer(source, el, media, p.Video)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", name, key, err)
		}
		layer.Element, layer.def, layer.source = key, el, source
		if key == "image" && role == "slide" {
			layer.AltText = str(item["alt_text"])
		}
		scene.Layers = append(scene.Layers, layer)
		return nil
	}

	if (role == "intro" || role == "outro") && item["image"] != nil && l["image"] == nil {
		source, err := mediaPath(base, item["image"], "image")
		if err != nil {
			return err
		}
		scene.Layers = append(scene.Layers, LayerPlan{Kind: "image", Path: source, Fit: str(choose(media["default_fit"], "cover")), X: 0, Y: 0, Width: p.Video.Width, Height: p.Video.Height, Opacity: 1, Element: "image", source: source})
	}

	if (role == "intro" || role == "outro") && l["image"] != nil || role == "slide" {
		if err := addMedia("image", item["image"]); err != nil {
			return err
		}
	}

	if logo != "" && l["logo"] != nil {
		if err := addMedia("logo", assets["logo"]); err != nil {
			return err
		}
	}

	for _, key := range []string{"title", "subtitle", "label", "text", "tip", "index", "cta"} {
		value := str(item[key])
		if key == "index" && role == "slide" {
			value = fmt.Sprintf("%d / %d", scene.index+1, scene.total)
		}
		if value == "" || l[key] == nil {
			continue
		}
		el := object(l[key])
		if el["visible"] == false {
			continue
		}
		layer, err := textLayer(value, key, el, typography, theme, p.Video)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", name, key, err)
		}
		layer.Element, layer.def, layer.source = key, el, value
		layer.Align = str(object(el["style"])["align"])
		scene.Layers = append(scene.Layers, layer)
	}

	if role == "slide" && l["progress"] != nil {
		if err := addProgress(scene, object(l["progress"]), scene.index+1, scene.total, theme, p.Video); err != nil {
			return fmt.Errorf("%s.progress: %w", name, err)
		}
	}

	scene.ASSPath = filepath.Join(p.WorkDir, "ass", fmt.Sprintf("scene-%04d.ass", len(p.Scenes)))
	scene.ASS = v2ASSDocument(p.Video, *scene)
	return nil
}

// compileCustomLayout turns a custom layout's elements into the scene's
// layout track, rendered in list order beneath any other track.
func (c *advancedContext) compileCustomLayout(scene *ScenePlan, sceneIndex int, elements []any) error {
	o := owner{scene: scene, index: sceneIndex, length: scene.Duration, bindings: scene.bindings}
	track := TrackPlan{ID: "layout", ASSPrefix: fmt.Sprintf("scene-%04d-layout", sceneIndex)}
	ids := map[string]bool{}
	for i, raw := range elements {
		el := object(raw)
		if id := str(el["id"]); id != "" {
			if ids[id] {
				return fmt.Errorf("duplicate element id %q", id)
			}
			ids[id] = true
		}
		if err := c.addClip(&track, el, o, 0); err != nil {
			label := fmt.Sprintf("element %d (%s)", i, el["type"])
			if id := str(el["id"]); id != "" {
				label = fmt.Sprintf("element %q", id)
			}
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	if err := attachToText(track.Clips, c.plan.Video); err != nil {
		return fmt.Errorf("element %w", err)
	}
	scene.Tracks = append([]TrackPlan{track}, scene.Tracks...)
	return nil
}

var (
	templateSchemaOnce sync.Once
	templateSchema     map[string]any
)

// validateDefinition checks a modified element definition against one of the
// template schema's definitions, so `set` cannot introduce unknown fields.
func validateDefinition(value obj, def string) error {
	templateSchemaOnce.Do(func() {
		raw, err := docs.Schemas.ReadFile("template.schema.json")
		if err == nil {
			_ = json.Unmarshal(raw, &templateSchema)
		}
	})
	rule, ok := object(templateSchema["$defs"])[def].(map[string]any)
	if !ok {
		return fmt.Errorf("schema definition %s missing", def)
	}
	return validate(normalizeNumbers(value), rule, templateSchema, "set")
}

// normalizeNumbers converts float64 values (from merges) to json.Number, the
// form the schema validator expects from parsed documents.
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range x {
			out[k] = normalizeNumbers(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeNumbers(item)
		}
		return out
	case float64:
		return json.Number(fmt.Sprint(x))
	}
	return v
}
