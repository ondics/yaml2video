package v2

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSchemaRejectsTemplateUnknownAndMissingRequired(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, _ := os.ReadFile(tmpl)
	for _, s := range []string{strings.Replace(string(raw), "format:\n", "format:\n  other: true\n", 1), strings.Replace(string(raw), "  fps: 30\n", "", 1)} {
		if err := os.WriteFile(tmpl, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(v, tmpl); err == nil {
			t.Fatal("accepted invalid template")
		}
	}
}

func TestSoundEffectAndProgress(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, _ := os.ReadFile(v)
	modified := strings.Replace(string(raw), "          duration: 3s\n", "          duration: 3s\n          sound_effects:\n              - path: assets/schulterkreisen.png\n                at: 1s\n                volume: 0.4\n", 1)
	if err := os.WriteFile(v, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(tmpl)
	modified = strings.Replace(string(raw), "    outro:\n", "        progress:\n            placement:\n                anchor: bottom-center\n            width_ratio: 0.5\n            thickness: 6\n    outro:\n", 1)
	if err := os.WriteFile(tmpl, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(v, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scenes[1].Audio) != 1 || p.Scenes[1].Audio[0].Offset != time.Second || !p.Scenes[1].Audio[0].SoundEffect {
		t.Errorf("audio events: %+v", p.Scenes[1].Audio)
	}
	layers := p.Scenes[1].Layers
	count := 0
	for _, l := range layers {
		if l.Kind == "rectangle" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("progress layers: %+v", layers)
	}

}

func TestDuplicateYAMLKeys(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, err := os.ReadFile(v)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(raw), "version: 2", "version: 2\nversion: 2", 1)
	if err = os.WriteFile(v, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(v, tmpl); err == nil {
		t.Fatal("accepted duplicate YAML key")
	}
}

func TestRejectsNumericOverflow(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(raw), "  width: 1080", "  width: 1e309", 1)
	if err = os.WriteFile(tmpl, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(v, tmpl); err == nil {
		t.Fatal("accepted non-finite numeric width")
	}
}

func TestRejectsExtraYAMLDocuments(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, err := os.ReadFile(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(v, append(raw, []byte("\n---\nversion: 2\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(v, tmpl); err == nil || !strings.Contains(err.Error(), "one YAML document") {
		t.Fatalf("expected document-count error, got %v", err)
	}
}

func TestNestedSchemaAndConditionalSectionRules(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"effect extra property", "                anchor: center\n            style:", "                anchor: center\n            effects:\n                - type: blur\n                  bogus: true\n            style:"},
		{"invalid slide source", "          source: content.slides", "          source: content.intro"},
		{"invalid duration type", "        slide_duration: 3s", "        slide_duration: true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, tmpl := example(t, "simple")
			raw, err := os.ReadFile(tmpl)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(raw), tc.old, tc.replacement, 1)
			if changed == string(raw) {
				t.Fatal("test fixture did not match")
			}
			if err = os.WriteFile(tmpl, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = Load(v, tmpl); err == nil {
				t.Fatal("accepted invalid schema")
			}
		})
	}
}

func TestInvalidEffectApplicability(t *testing.T) {
	v, tmpl := example(t, "simple")
	raw, _ := os.ReadFile(tmpl)
	modified := strings.Replace(string(raw), "            max_lines: 3\n", "            effects:\n                - type: pan_zoom\n            max_lines: 3\n", 1)
	if err := os.WriteFile(tmpl, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(v, tmpl)
	if err == nil || !strings.Contains(err.Error(), "not applicable") {
		t.Fatalf("expected effect applicability error, got %v", err)
	}
}
