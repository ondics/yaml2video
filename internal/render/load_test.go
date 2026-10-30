package render

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadTemplateFile(t *testing.T) {
	for _, name := range []string{"video-relative", "work-dir", "absolute", "explicit override", "missing selection", "missing file", "mismatched ID"} {
		t.Run(name, func(t *testing.T) {
			videoPath, templatePath := example(t, "simple")
			want, err := Load(videoPath, templatePath)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(videoPath)
			if err != nil {
				t.Fatal(err)
			}
			// Remove the example's reference so each case controls selection.
			raw = []byte(strings.ReplaceAll(string(raw), "template_file: template.yaml\n", ""))
			reference := filepath.Base(templatePath)
			base, explicit, message := "", "", ""
			switch name {
			case "work-dir":
				base = t.TempDir()
				data, err := os.ReadFile(templatePath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(base, reference), data, 0600); err != nil {
					t.Fatal(err)
				}
				// Prove the override doesn't fall back to the video's directory.
				if err := os.Remove(templatePath); err != nil {
					t.Fatal(err)
				}
			case "absolute":
				reference = templatePath
				base = t.TempDir()
			case "explicit override":
				reference = "does-not-exist.yaml"
				explicit = templatePath
				base = t.TempDir()
			case "missing selection":
				reference = ""
				message = "set template_file"
			case "missing file":
				reference = "does-not-exist.yaml"
				message = "does-not-exist.yaml"
			case "mismatched ID":
				raw = []byte(strings.Replace(string(raw), "office-mobility-simple", "other", 1))
				message = "does not match"
			}
			if reference != "" {
				raw = append(raw, []byte("\ntemplate_file: "+reference+"\n")...)
			}
			if err := os.WriteFile(videoPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadWithTemplateBase(videoPath, explicit, base)
			if message != "" {
				if err == nil || !strings.Contains(err.Error(), message) {
					t.Fatalf("expected %q error, got %v", message, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("template reference changed the plan or media paths")
			}
			if name == "video-relative" {
				if _, err := Load(videoPath, ""); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestLoadBytes(t *testing.T) {
	videoPath, templatePath := example(t, "simple")
	want, err := Load(videoPath, templatePath)
	if err != nil {
		t.Fatal(err)
	}
	videoData, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{videoPath, templatePath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	got, err := FromBytes(videoData, templateData, filepath.Dir(videoPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("byte-based plan differs from file-based plan")
	}

	for _, tc := range []struct {
		name     string
		video    []byte
		template []byte
		message  string
	}{
		{"invalid video YAML", []byte("["), templateData, "video: YAML:"},
		{"invalid template YAML", videoData, []byte("["), "template: YAML:"},
		{"invalid video schema", []byte("version: 2\nunexpected: true\n"), templateData, "video:"},
		{"invalid template schema", videoData, []byte("version: 2\nunexpected: true\n"), "template:"},
		{"extra document", append(append([]byte(nil), videoData...), []byte("\n---\nversion: 2\n")...), templateData, "one YAML document"},
		{"template mismatch", []byte(strings.Replace(string(videoData), "office-mobility-simple", "other", 1)), templateData, "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromBytes(tc.video, tc.template, filepath.Dir(videoPath))
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q error, got %v", tc.message, err)
			}
		})
	}
}
