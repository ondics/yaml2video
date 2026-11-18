package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// GeneratePreview extracts the first decoded video frame as a full-resolution PNG.
func GeneratePreview(ctx context.Context, videoPath, previewPath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("generate preview: %w", err)
	}

	// A fresh output prevents a previous preview from hiding a no-frame result.
	output, err := os.CreateTemp(filepath.Dir(previewPath), ".preview-*.png")
	if err != nil {
		return fmt.Errorf("create preview output %q: %w", previewPath, err)
	}
	tempPath := output.Name()
	defer os.Remove(tempPath)
	if err := output.Close(); err != nil {
		return fmt.Errorf("close preview output: %w", err)
	}

	err = runFFmpeg(ctx, []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-i", videoPath, "-map", "0:v:0", "-frames:v", "1",
		"-c:v", "png", "-f", "image2", "-update", "1", tempPath,
	}, nil)
	if err != nil {
		return fmt.Errorf("generate preview from %q: %w", videoPath, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("generate preview: %w", err)
	}
	info, err := os.Stat(tempPath)
	if err != nil {
		return fmt.Errorf("check preview output for %q: %w", videoPath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("generate preview from %q: ffmpeg produced no frame (empty PNG output)", videoPath)
	}
	if err := os.Rename(tempPath, previewPath); err != nil {
		return fmt.Errorf("save preview %q: %w", previewPath, err)
	}
	return nil
}
