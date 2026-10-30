package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The showcase's scene templates set every scene's length and transition, so
// its scenes start on a 120 BPM beat grid.
func TestShowcaseExample(t *testing.T) {
	base := filepath.Join("..", "..", "examples", "showcase")
	video, err := os.ReadFile(filepath.Join(base, "video.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := os.ReadFile(filepath.Join(base, "template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := FromBytes(video, bundledExampleFonts(tmpl), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scenes) != 16 || p.Duration != 36500*time.Millisecond {
		t.Fatalf("scenes=%d duration=%s", len(p.Scenes), p.Duration)
	}
	for _, scene := range p.Scenes {
		if scene.Start%(500*time.Millisecond) != 0 {
			t.Errorf("%s starts off the beat at %s", scene.ID, scene.Start)
		}
	}
	if _, err := p.Commands(); err != nil {
		t.Fatal(err)
	}
}

func TestExamplePlans(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		scenes                                         int
		duration                                       time.Duration
		firstTransition, slideTransition, musicFadeOut time.Duration
	}{
		{"simple", 5, 13600 * time.Millisecond, 350 * time.Millisecond, 350 * time.Millisecond, 800 * time.Millisecond},
		{"normal", 6, 16150 * time.Millisecond, 450 * time.Millisecond, 350 * time.Millisecond, time.Second},
		{"complex", 8, 23250 * time.Millisecond, 250 * time.Millisecond, 250 * time.Millisecond, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			video, tmpl := example(t, tc.name)
			p, err := Load(video, tmpl)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if len(p.Scenes) != tc.scenes || len(p.Transitions) != tc.scenes-1 {
				t.Fatalf("scenes=%d transitions=%d", len(p.Scenes), len(p.Transitions))
			}
			if p.Duration != tc.duration {
				t.Errorf("duration=%s want %s", p.Duration, tc.duration)
			}
			elapsed := time.Duration(0)
			for i, scene := range p.Scenes {
				if scene.Start != elapsed {
					t.Errorf("scene %d starts %s want %s", i, scene.Start, elapsed)
				}
				if scene.Duration <= 0 || scene.Output == "" || scene.ASS == "" || scene.ASSPath == "" {
					t.Errorf("scene %d missing artifacts or duration: %+v", i, scene)
				}
				elapsed += scene.Duration
				if i < len(p.Transitions) {
					tr := p.Transitions[i]
					if tr.FromScene != i || tr.Offset != p.Scenes[i+1].Start {
						t.Errorf("transition %d: %+v", i, tr)
					}
					expected := tc.slideTransition
					if i == 0 {
						expected = tc.firstTransition
					}
					if tr.Duration != expected {
						t.Errorf("transition %d=%s want %s", i, tr.Duration, expected)
					}
					elapsed -= tr.Duration
				}
			}
			if elapsed != p.Duration {
				t.Errorf("timeline sum %s != plan %s", elapsed, p.Duration)
			}
			if p.Music == nil || p.Music.Path == "" || p.Music.FadeOut != tc.musicFadeOut {
				t.Errorf("music: %+v", p.Music)
			}
			commands, err := p.Commands()
			if err != nil {
				t.Fatalf("commands: %v", err)
			}
			if len(commands) != tc.scenes+1 {
				t.Errorf("commands=%d want %d", len(commands), tc.scenes+1)
			}
			if tc.name == "normal" {
				assertNormal(t, p, commands)
			}
			if tc.name == "complex" {
				assertComplex(t, p, commands)
			}
		})
	}
}

func exampleImage(scene ScenePlan) (LayerPlan, bool) {
	for _, l := range scene.Layers {
		if l.Kind == "image" && strings.Contains(l.Path, "assets/") && strings.Contains(l.Path, ".png") && l.Shape != "rectangle" {
			return l, true
		}
	}
	return LayerPlan{}, false
}

func assertNormal(t *testing.T, p *Plan, commands [][]string) {
	t.Helper()
	if p.Music.Volume != .35 || p.Music.FadeIn != 300*time.Millisecond {
		t.Errorf("normal music overrides: %+v", p.Music)
	}
	if p.Scenes[0].Layers[0].Width != p.Video.Width {
		t.Error("intro image not composed as background")
	}
	layer, ok := exampleImage(p.Scenes[1])
	if !ok || layer.Shape != "rounded-rectangle" || layer.AltText == "" {
		t.Errorf("normal slide image metadata: %+v", layer)
	}
	if len(layer.Effects) != 2 || layer.Effects[0].Type != "pan_zoom" || layer.Effects[1].Type != "vignette" {
		t.Errorf("ordered image effects: %+v", layer.Effects)
	}
	graph := strings.Join(commands[1], " ")
	for _, want := range []string{"zoompan", "vignette", "gblur"} {
		if !strings.Contains(graph, want) {
			t.Errorf("normal command missing %s", want)
		}
	}
	textEffect := false
	for _, l := range p.Scenes[1].Layers {
		for _, fx := range l.Effects {
			if fx.Type == "glass_panel" && fx.Blur == 10 {
				textEffect = true
			}
		}
	}
	if !textEffect {
		t.Error("normal text backdrop blur not mapped")
	}
}

func assertComplex(t *testing.T, p *Plan, commands [][]string) {
	t.Helper()
	if p.Music.Ducking == nil || !p.Music.Ducking.Enabled || p.Music.Ducking.Amount != .55 || !p.Music.Normalize {
		t.Errorf("music ducking/normalize: %+v", p.Music)
	}
	if p.Music.FadeIn != 500*time.Millisecond {
		t.Errorf("music fade: %+v", p.Music)
	}
	for i := 1; i <= 6; i++ {
		layer, ok := exampleImage(p.Scenes[i])
		if !ok || layer.Shape != "rounded-rectangle" || layer.AltText == "" {
			t.Errorf("slide %d image/accessibility: %+v", i, layer)
		}
		if !strings.Contains(p.Scenes[i].ASS, fmt.Sprintf("%d / 6", i)) {
			t.Errorf("slide %d index not rendered", i)
		}
	}
	if len(p.Scenes[3].Audio) != 1 || p.Scenes[3].Audio[0].Offset != 0 || p.Scenes[3].Audio[0].Volume != .45 || p.Scenes[3].Audio[0].FadeIn != 500*time.Millisecond {
		t.Errorf("sound effect: %+v", p.Scenes[3].Audio)
	}
	if len(p.Scenes[5].Audio) != 1 || p.Scenes[5].Audio[0].Volume != .8 || p.Scenes[5].Audio[0].FadeIn != 500*time.Millisecond {
		t.Errorf("foreground audio: %+v", p.Scenes[5].Audio)
	}
	graph := strings.Join(commands[len(commands)-1], " ")
	for _, want := range []string{"amix", "sidechaincompress"} {
		if !strings.Contains(graph, want) {
			t.Errorf("final command missing %s", want)
		}
	}
}
