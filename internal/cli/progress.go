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
	jobs, err := render.SceneJobs()
	if err != nil {
		fmt.Fprintf(progressOutput, "[Warning] %v\n", err)
	}
	fmt.Fprintf(progressOutput, "Rendering %d scenes (%dx%d at %d fps, %s), %s\n", len(plan.Scenes), plan.Video.Width, plan.Video.Height, plan.Video.FPS, plan.Duration,
		parallelism(jobs, len(plan.Scenes), err == nil && os.Getenv(render.SceneJobsEnv) != ""))
	if err := plan.RenderWithProgress(ctx, cliProgress(progressOutput, len(plan.Scenes))); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Output: %s\n", relativeOutputPath(plan.Output))
	return err
}

// parallelism describes how many scenes render at once and why.
func parallelism(jobs, scenes int, fromEnv bool) string {
	source := fmt.Sprintf("default: one per CPU core, up to 4; set %s to change", render.SceneJobsEnv)
	if fromEnv {
		source = fmt.Sprintf("%s=%d", render.SceneJobsEnv, jobs)
	}
	if scenes > 0 && jobs > scenes {
		return fmt.Sprintf("%d at a time, one per scene (%s)", scenes, source)
	}
	return fmt.Sprintf("%d at a time (%s)", jobs, source)
}

// cliProgress prints render progress. Scene commands come first, so the
// completed commands during the scene stage are completed scenes.
func cliProgress(output io.Writer, scenes int) func(render.Progress) {
	lastPercent, lastCompleted, lastStage := -1, -1, ""
	return func(p render.Progress) {
		percent := int(p.Percent)
		if percent == lastPercent && p.Completed == lastCompleted && p.Stage == lastStage {
			return
		}

		lastPercent, lastCompleted, lastStage = percent, p.Completed, p.Stage
		switch p.Stage {
		case "scene":
			fmt.Fprintf(output, "%3d%% Rendering scenes (%d/%d complete)\n", percent, p.Completed, scenes)
		case "final":
			fmt.Fprintf(output, "%3d%% Finishing video\n", percent)
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
