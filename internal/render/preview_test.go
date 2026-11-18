package render

import (
	"context"
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func previewFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args = append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v\n%s", err, output)
	}
}

func TestGeneratePreviewFirstFrame(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "two colors.mp4")
	preview := filepath.Join(dir, "preview.png")
	// Only the first frame is red; all subsequent frames are blue.
	previewFFmpeg(t, "-f", "lavfi", "-i", "color=c=red:s=96x64:r=10:d=0.1",
		"-f", "lavfi", "-i", "color=c=blue:s=96x64:r=10:d=0.9",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]",
		"-c:v", "mpeg4", "-q:v", "2", video)
	if err := os.WriteFile(preview, []byte("old preview"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := GeneratePreview(ctx, video, preview); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(preview)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if bounds := img.Bounds(); bounds.Dx() != 96 || bounds.Dy() != 64 {
		t.Fatalf("preview dimensions = %v, want 96x64", bounds)
	}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// Allow for the MP4's lossy encoding and YUV-to-RGB conversion.
			if r < 240*257 || g > 15*257 || b > 15*257 || a != 65535 {
				t.Fatalf("pixel (%d,%d) = (%d,%d,%d,%d), want opaque red", x, y, r, g, b, a)
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected only video and preview, got %v", entries)
	}
}

func TestGeneratePreviewInvalidVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, name := range []string{"missing", "empty", "no frames"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			video := filepath.Join(dir, "input.mp4")
			preview := filepath.Join(dir, "preview.png")
			switch name {
			case "empty":
				if err := os.WriteFile(video, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "no frames":
				previewFFmpeg(t, "-f", "lavfi", "-i", "color=c=red:s=96x64",
					"-frames:v", "0", "-c:v", "mpeg4", "-movflags", "empty_moov", video)
			}
			// A stale preview must not turn an empty extraction into success.
			if err := os.WriteFile(preview, []byte("old preview"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := GeneratePreview(ctx, video, preview)
			if err == nil || !strings.Contains(err.Error(), video) {
				t.Fatalf("want useful error containing input path, got %v", err)
			}

			data, err := os.ReadFile(preview)
			if err != nil || string(data) != "old preview" {
				t.Errorf("failed extraction changed existing preview: %q, %v", data, err)
			}
		})
	}
}

func TestGeneratePreviewNoOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell executable")
	}
	dir := t.TempDir()
	// FFmpeg versions differ on whether a stream with no frames exits zero.
	// Simulate a successful process without output to exercise the size check.
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	err := GeneratePreview(context.Background(), "input.mp4", filepath.Join(dir, "preview.png"))
	if err == nil || !strings.Contains(err.Error(), "no frame") {
		t.Fatalf("want no-frame error after zero exit, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("failed extraction left output behind: %v", entries)
	}
}

func TestGeneratePreviewCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "canceled"
		want := context.Canceled
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if deadline {
			name = "deadline"
			want = context.DeadlineExceeded
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
		}
		t.Run(name, func(t *testing.T) {
			preview := filepath.Join(t.TempDir(), "preview.png")
			err := GeneratePreview(ctx, "input.mp4", preview)
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want wrapped %v", err, want)
			}
			if _, err := os.Stat(preview); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("canceled extraction created output: %v", err)
			}
		})
	}
}
