package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

func TestCLIMissingFont(t *testing.T) {
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	t.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	const family = "YAML2Video Nonexistent CLI Regression Font"
	directory := t.TempDir()
	for _, name := range []string{"video", "template"} {
		data, err := os.ReadFile(filepath.Join("..", "examples", "simple", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		if name == "video" {
			delete(document, "assets")
		} else {
			document["defaults"].(map[string]any)["typography"].(map[string]any)["font_family"] = family
			// Only text scenes: a missing font must fail before FFmpeg runs.
			composition := document["composition"].(map[string]any)
			sections := composition["sections"].([]any)
			composition["sections"] = []any{sections[0], sections[2]}
		}
		data, err = yaml.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name+".yaml"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	binary := filepath.Join(directory, "yaml2video")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	for _, mode := range []string{"render", "dry-run"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{filepath.Join(directory, "video.yaml")}
			if mode == "dry-run" {
				args = append([]string{"-n"}, args...)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, args...)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("CLI error = %v, want exit 1; stderr: %s", err, &stderr)
			}
			for _, want := range []string{"[Error]", family, "Go Mono"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q: %s", want, &stderr)
				}
			}
			if !strings.Contains(strings.ReplaceAll(stderr.String(), "Go Mono", ""), "Go") {
				t.Errorf("stderr does not suggest Go separately from Go Mono: %s", &stderr)
			}
			for _, unwanted := range []string{"panic:", "goroutine ", "Rendering "} {
				if strings.Contains(stderr.String(), unwanted) {
					t.Errorf("stderr unexpectedly contains %q: %s", unwanted, &stderr)
				}
			}
			if stdout.Len() != 0 {
				t.Errorf("unexpected success output: %s", &stdout)
			}
		})
	}
}
