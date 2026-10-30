package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ondics/yaml2video/internal/render"
)

func renderCLI(ctx context.Context, plan *render.Plan, progressOutput, output io.Writer) error {
	fmt.Fprintf(progressOutput, "Rendering %d scenes (%dx%d at %d fps, %s)\n", len(plan.Scenes), plan.Video.Width, plan.Video.Height, plan.Video.FPS, plan.Duration)
	if err := plan.RenderWithProgress(ctx, cliProgress(progressOutput)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "Output: %s\n", relativeOutputPath(plan.Output))
	return err
}

func cliProgress(output io.Writer) func(render.Progress) {
	lastPercent, lastCompleted, lastStage := -1, -1, ""
	return func(p render.Progress) {
		percent := int(p.Percent)
		if percent == lastPercent && p.Completed == lastCompleted && p.Stage == lastStage {
			return
		}

		lastPercent, lastCompleted, lastStage = percent, p.Completed, p.Stage
		switch p.Stage {
		case "scene":
			fmt.Fprintf(output, "%3d%% Rendering scenes (%d/%d complete)\n", percent, p.Completed, p.Total-1)
		case "final":
			fmt.Fprintf(output, "%3d%% Encoding final video\n", percent)
		case "done":
			fmt.Fprintf(output, "%3d%% Render complete\n", percent)
		}
	}
}

func relativeOutputPath(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}

	relative, err := filepath.Rel(cwd, absolute)
	if err != nil {
		// Windows paths on different volumes cannot be made relative.
		return path
	}
	return relative
}
