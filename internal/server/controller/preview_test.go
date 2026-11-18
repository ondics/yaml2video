package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreviewFailureRemovesRender(t *testing.T) {
	c := testController(t)
	failure := errors.New("preview extraction failed")
	c.preview = func(_ context.Context, video, preview string) error {
		if _, err := os.Stat(video); err != nil {
			t.Fatalf("video must exist before preview generation: %v", err)
		}
		if metadataFor(t, c, "failure").Ready {
			t.Fatal("job ready before preview generation")
		}
		if err := os.WriteFile(preview, []byte("partial PNG"), 0o600); err != nil {
			t.Fatal(err)
		}
		return failure
	}
	if err := renderOutput(t, c, "failure"); !errors.Is(err, failure) {
		t.Fatalf("Run error = %v, want preview failure", err)
	}
	if _, err := os.Stat(c.jobDir("failure")); !os.IsNotExist(err) {
		t.Fatalf("failed job files retained: %v", err)
	}
	if len(c.active) != 0 {
		t.Fatalf("failed job reservation retained: %v", c.active)
	}
}

func TestPreviewSharesCleanupAndActiveProtection(t *testing.T) {
	c := testController(t)
	if err := renderOutput(t, c, "movie"); err != nil {
		t.Fatal(err)
	}
	artifact, err := c.OpenPreview("movie")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if artifact != nil {
			artifact.Close()
		}
	}()
	m := metadataFor(t, c, "movie")
	if m.FreeableAt != nil {
		t.Fatal("opening preview shortened retention")
	}
	m.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
	if err := writeMetadata(c.jobDir("movie"), m); err != nil {
		t.Fatal(err)
	}
	if err := c.cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"output.mp4", "preview.png"} {
		if _, err := os.Stat(filepath.Join(c.jobDir("movie"), name)); err != nil {
			t.Fatalf("active preview did not protect %s: %v", name, err)
		}
	}
	artifact.Close()
	artifact = nil
	if err := c.cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.jobDir("movie")); !os.IsNotExist(err) {
		t.Fatalf("expired video and preview retained: %v", err)
	}
}
