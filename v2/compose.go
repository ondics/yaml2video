// Package v2 loads and composes version 2 video and template documents.
package v2

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ondics/yaml2video/render"
)

type obj = map[string]any

func object(v any) obj {
	x, _ := v.(map[string]any)
	if x == nil {
		return obj{}
	}
	return x
}

func str(v any) string  { s, _ := v.(string); return s }
func num(v any) float64 { n, _ := number(v); return n }
func integer(v any) int { return int(num(v)) }
func flag(v any) bool   { return v == true }

func duration(v any) (time.Duration, error) {
	if v == nil {
		return 0, nil
	}
	var seconds float64
	if s, ok := v.(string); ok {
		seconds, _ = strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
	} else {
		seconds = num(v)
	}
	if seconds <= 0 || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0, fmt.Errorf("invalid duration %v", v)
	}
	return time.Duration(math.Round(seconds * float64(time.Second))), nil
}

func offset(v any) (time.Duration, error) {
	if v == nil || v == json.Number("0") || v == "0s" {
		return 0, nil
	}
	return duration(v)
}

func choose(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func path(base string, v any) (string, error) {
	s := str(v)
	if s == "" {
		return "", nil
	}
	if strings.Contains(s, "://") {
		return "", fmt.Errorf("unsupported media URI %q", s)
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(base, s)
	}

	s = filepath.Clean(s)
	f, err := os.Open(s)
	if err != nil {
		return "", fmt.Errorf("media %s: %w", s, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() {
		return "", fmt.Errorf("media %s is not a regular file", s)
	}

	return s, nil
}

// Load validates both explicit documents and builds a render plan. Paths to
// media are relative to videoPath. Work artifacts are placed in .yaml2video-v2.
func Load(videoPath, templatePath string) (*render.Plan, error) {
	v, e := document(videoPath, "video.schema.json")
	if e != nil {
		return nil, e
	}
	t, e := document(templatePath, "template.schema.json")
	if e != nil {
		return nil, e
	}
	if str(v["template"]) != str(t["template_id"]) {
		return nil, fmt.Errorf("video template %q does not match template_id %q", v["template"], t["template_id"])
	}
	return compose(v, t, filepath.Dir(videoPath))
}
func compose(v, t obj, base string) (*render.Plan, error) {
	format := object(t["format"])
	defaults := object(t["defaults"])
	theme := object(defaults["theme"])
	timing := object(defaults["timing"])
	typography := object(defaults["typography"])
	audioDefaults := object(defaults["audio"])
	layout := object(t["layout"])
	content := object(v["content"])
	assets := object(v["assets"])
	p := &render.Plan{Video: render.VideoSpec{Width: integer(format["width"]), Height: integer(format["height"]), FPS: integer(format["fps"]), Background: str(theme["background"])}, WorkDir: filepath.Join(base, ".yaml2video-v2"), Output: filepath.Join(base, "output.mp4")}

	logo, err := path(base, assets["logo"])
	if err != nil {
		return nil, err
	}

	sections := object(t["composition"])["sections"].([]any)

	type boundary struct {
		transition obj
		index      int
	}
	var boundaries []boundary
	ids := map[string]bool{}
	sceneIDs := map[string]bool{}
	hasSlides := false
	for _, raw := range sections {
		section := object(raw)
		id := str(section["id"])
		if ids[id] {
			return nil, fmt.Errorf("duplicate section id %s", id)
		}

		ids[id] = true
		role := str(section["role"])

		var items []any
		switch role {
		case "intro", "outro":
			if item := content[role]; item != nil {
				items = []any{item}
			}
		case "slide":
			hasSlides = true
			items = content["slides"].([]any)
		}
		if role != "slide" && flag(section["repeat"]) {
			return nil, fmt.Errorf("%s: repeat is only supported for slides", id)
		}
		for i, rawItem := range items {
			item := object(rawItem)
			name := id
			if role == "slide" {
				name = fmt.Sprintf("%s-%d", id, i+1)
				if s := str(item["id"]); s != "" {
					name = id + "-" + s
				}
			}

			if sceneIDs[name] {
				return nil, fmt.Errorf("duplicate scene id %q", name)
			}
			sceneIDs[name] = true

			d, err := duration(choose(item["duration"], section["default_duration"], timing[role+"_duration"]))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}

			scene := render.ScenePlan{ID: name, Duration: d, Background: str(object(layout[role])["background"])}
			l := object(layout[role])
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
				source, err := path(base, asset)
				if err != nil {
					return err
				}
				layer, err := mediaLayer(source, el, object(defaults["media"]), p.Video)
				if key == "image" && role == "slide" {
					layer.AltText = str(item["alt_text"])
				}
				if err != nil {
					return fmt.Errorf("%s.%s: %w", name, key, err)
				}
				scene.Layers = append(scene.Layers, layer)
				return nil
			}

			if (role == "intro" || role == "outro") && item["image"] != nil && l["image"] == nil {
				source, err := path(base, item["image"])
				if err != nil {
					return nil, err
				}
				scene.Layers = append(scene.Layers, render.LayerPlan{Kind: "image", Path: source, Fit: str(choose(object(defaults["media"])["default_fit"], "cover")), X: 0, Y: 0, Width: p.Video.Width, Height: p.Video.Height, Opacity: 1})
			}

			if (role == "intro" || role == "outro") && l["image"] != nil {
				if err = addMedia("image", item["image"]); err != nil {
					return nil, err
				}
			} else if role == "slide" {
				if err = addMedia("image", item["image"]); err != nil {
					return nil, err
				}
			}

			if logo != "" && l["logo"] != nil {
				if err = addMedia("logo", assets["logo"]); err != nil {
					return nil, err
				}
			}

			alignments := map[int]string{}
			keys := []string{"title", "subtitle", "label", "text", "tip", "index", "cta"}
			for _, key := range keys {
				value := str(item[key])
				if key == "index" && role == "slide" {
					value = fmt.Sprintf("%d / %d", i+1, len(items))
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
					return nil, fmt.Errorf("%s.%s: %w", name, key, err)
				}
				alignments[len(scene.Layers)] = str(object(el["style"])["align"])
				scene.Layers = append(scene.Layers, layer)
			}

			if role == "slide" && l["progress"] != nil {
				if err := addProgress(&scene, object(l["progress"]), i+1, len(items), theme, p.Video); err != nil {
					return nil, fmt.Errorf("%s.progress: %w", name, err)
				}
			}

			track := object(item["audio"])
			if len(track) > 0 {
				a, err := audioLayer(track, audioDefaults, base, 0, d)
				if err != nil {
					return nil, fmt.Errorf("%s.audio: %w", name, err)
				}
				scene.Audio = append(scene.Audio, a)
			}

			if effects, ok := item["sound_effects"].([]any); ok {
				for j, raw := range effects {
					fx := object(raw)
					at, err := offset(fx["at"])
					if err != nil || at >= d {
						return nil, fmt.Errorf("%s.sound_effects[%d]: offset outside scene", name, j)
					}

					a, err := audioLayer(fx, audioDefaults, base, at, d-at)
					if err != nil {
						return nil, fmt.Errorf("%s.sound_effects[%d]: %w", name, j, err)
					}
					a.SoundEffect = true

					scene.Audio = append(scene.Audio, a)
				}
			}

			scene.ASSPath = filepath.Join(p.WorkDir, "ass", fmt.Sprintf("scene-%04d.ass", len(p.Scenes)))
			scene.ASS = assDocument(p.Video, scene, alignments)
			scene.Output = filepath.Join(p.WorkDir, "scenes", fmt.Sprintf("scene-%04d.mkv", len(p.Scenes)))
			p.Scenes = append(p.Scenes, scene)
			tr := object(choose(section["transition_out"], timing["transition"]))
			boundaries = append(boundaries, boundary{tr, len(p.Scenes) - 1})
		}
	}

	if !hasSlides {
		return nil, fmt.Errorf("composition must include a slide section")
	}

	if len(p.Scenes) == 0 {
		return nil, fmt.Errorf("composition has no scenes")
	}

	if object(sections[len(sections)-1])["transition_out"] != nil {
		return nil, fmt.Errorf("transition on final section")
	}

	for i, b := range boundaries {
		if i == len(boundaries)-1 {
			break
		}

		tr := b.transition
		if str(tr["type"]) == "cut" {
			continue
		}

		d, err := duration(tr["duration"])
		if err != nil {
			return nil, err
		}

		if d >= p.Scenes[b.index].Duration || d >= p.Scenes[b.index+1].Duration {
			return nil, fmt.Errorf("transition exceeds adjacent scene duration")
		}
		p.Transitions = append(p.Transitions, render.BoundaryTransition{FromScene: b.index, Type: str(tr["type"]), Duration: d})
	}

	for i := range p.Scenes {
		if i > 0 {
			p.Scenes[i].Start = p.Scenes[i-1].Start + p.Scenes[i-1].Duration
			for _, tr := range p.Transitions {
				if tr.FromScene == i-1 {
					p.Scenes[i].Start -= tr.Duration
				}
			}
		}

		p.Duration = p.Scenes[i].Start + p.Scenes[i].Duration
	}

	for i := range p.Transitions {
		p.Transitions[i].Offset = p.Scenes[p.Transitions[i].FromScene+1].Start
	}

	if music := object(assets["music"]); len(music) > 0 {
		source, err := path(base, music["path"])
		if err != nil {
			return nil, err
		}

		fadeIn, err := duration(choose(music["fade_in"], audioDefaults["fade_in"]))
		if err != nil {
			return nil, err
		}

		fadeOut, err := duration(choose(music["fade_out"], audioDefaults["fade_out"]))
		if err != nil {
			return nil, err
		}
		if fadeOut > p.Duration || fadeIn > p.Duration {
			return nil, fmt.Errorf("music fade exceeds video duration")
		}

		volume := num(choose(music["volume"], audioDefaults["default_volume"], json.Number("1")))
		p.Music = &render.MusicPlan{Path: source, Volume: volume, FadeIn: fadeIn, FadeOut: fadeOut, Normalize: audioDefaults["normalize"] != false}
		if duck := object(audioDefaults["ducking"]); flag(duck["enabled"]) {
			attack, err := duration(choose(duck["attack"], "0.15s"))
			if err != nil {
				return nil, fmt.Errorf("music ducking attack: %w", err)
			}

			release, err := duration(choose(duck["release"], "0.4s"))
			if err != nil {
				return nil, fmt.Errorf("music ducking release: %w", err)
			}

			p.Music.Ducking = &render.DuckingPlan{Enabled: true, Amount: num(choose(duck["amount"], json.Number("0.65"))), Attack: attack, Release: release}
		}
	}

	return p, nil
}

func audioLayer(track, defaults obj, base string, at, remaining time.Duration) (render.AudioLayerPlan, error) {
	source, err := path(base, track["path"])
	if err != nil {
		return render.AudioLayerPlan{}, err
	}

	fi, err := duration(choose(track["fade_in"], defaults["fade_in"]))
	if err != nil {
		return render.AudioLayerPlan{}, err
	}

	fo, err := duration(choose(track["fade_out"], defaults["fade_out"]))
	if err != nil {
		return render.AudioLayerPlan{}, err
	}
	if fi > remaining || fo > remaining {
		return render.AudioLayerPlan{}, fmt.Errorf("audio fade exceeds remaining scene duration")
	}

	return render.AudioLayerPlan{Path: source, Volume: num(choose(track["volume"], defaults["default_volume"], json.Number("1"))), Offset: at, Duration: remaining, FadeIn: fi, FadeOut: fo}, nil
}
