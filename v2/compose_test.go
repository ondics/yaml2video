package v2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ondics/yaml2video/render"
)

func example(t *testing.T, kind string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "examples", kind)
	for _, file := range []string{"video-" + kind + ".yaml", "template-" + kind + ".yaml"} {
		raw, err := os.ReadFile(filepath.Join(src, file))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, file), raw, 0600); err != nil {
			t.Fatal(err)
		}

		for line := range strings.SplitSeq(string(raw), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			if strings.HasPrefix(line, "path: assets/") || strings.HasPrefix(line, "image: assets/") || strings.HasPrefix(line, "logo: assets/") {
				p := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				if err = os.MkdirAll(filepath.Join(dir, "assets"), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, p), []byte("stub"), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	return filepath.Join(dir, "video-"+kind+".yaml"), filepath.Join(dir, "template-"+kind+".yaml")
}

func TestSimpleExample(t *testing.T) {
	v, tmpl := example(t, "simple")
	plan, err := Load(v, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Scenes) != 5 || len(plan.Transitions) != 4 {
		t.Fatalf("scenes=%d transitions=%d", len(plan.Scenes), len(plan.Transitions))
	}
	if plan.Scenes[1].Start != plan.Scenes[0].Duration-plan.Transitions[0].Duration {
		t.Fatalf("wrong overlap: %+v", plan.Scenes[1])
	}
	if plan.Duration != 13600*time.Millisecond {
		t.Errorf("duration %s", plan.Duration)
	}
	if plan.Music == nil || plan.Music.FadeIn != 200*time.Millisecond {
		t.Errorf("music defaults: %+v", plan.Music)
	}
	if _, err := plan.Commands(); err != nil {
		t.Fatalf("render plan commands: %v", err)
	}
	if len(plan.Scenes[1].Layers) < 3 || !strings.Contains(plan.Scenes[1].ASS, "Schultern kreisen") {
		t.Errorf("slide visual bindings missing")
	}
}
func TestSchemaRejectsUnknownAndWrongTypes(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, err := os.ReadFile(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"version: 3", "version: two", "unexpected: true"} {
		s := strings.Replace(string(raw), "version: 2", "version: 2\n"+change, 1)
		if change != "unexpected: true" {
			s = strings.Replace(string(raw), "version: 2", change, 1)
		}
		if err = os.WriteFile(v, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(v, tmpl); err == nil {
			t.Errorf("accepted %s", change)
		}
	}
}

func TestMediaPlacementAnchors(t *testing.T) {
	canvas := render.VideoSpec{Width: 1080, Height: 1920}
	for _, tc := range []struct {
		anchor string
		x, y   int
	}{
		{"top-left", 64, 80}, {"top-center", 496, 80}, {"top-right", 928, 80},
		{"center-left", 64, 848}, {"center", 496, 848}, {"center-right", 928, 848},
		{"bottom-left", 64, 1616}, {"bottom-center", 496, 1616}, {"bottom-right", 928, 1616},
	} {
		t.Run(tc.anchor, func(t *testing.T) {
			el := obj{"placement": obj{"anchor": tc.anchor, "offset_x": float64(64), "offset_y": float64(80)}, "max_width_ratio": float64(.2), "max_height_ratio": float64(.2), "fit": "contain"}
			layer, err := mediaLayer("logo.png", el, obj{}, canvas)
			if err != nil {
				t.Fatal(err)
			}
			if layer.X != tc.x || layer.Y != tc.y || layer.Anchor != tc.anchor {
				t.Errorf("got (%d, %d, %q), want (%d, %d, %q)", layer.X, layer.Y, layer.Anchor, tc.x, tc.y, tc.anchor)
			}
		})
	}
}

func TestTemplateMismatch(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, _ := os.ReadFile(v)
	if err := os.WriteFile(v, []byte(strings.Replace(string(raw), "office-mobility-simple", "other", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(v, tmpl); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatch: %v", err)
	}
}
