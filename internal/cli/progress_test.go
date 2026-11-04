package cli

import (
	"strings"
	"testing"

	"github.com/ondics/yaml2video/internal/render"
)

func TestParallelismNamesItsSource(t *testing.T) {
	for _, tc := range []struct {
		jobs, scenes int
		fromEnv      bool
		want         string
	}{
		{2, 6, true, "2 at a time (YAML2VIDEO_SCENE_JOBS=2)"},
		{4, 6, false, "4 at a time (default: one per CPU core, up to 4; set YAML2VIDEO_SCENE_JOBS to change)"},
		{8, 3, true, "3 at a time, one per scene (YAML2VIDEO_SCENE_JOBS=8)"},
	} {
		if got := parallelism(tc.jobs, tc.scenes, tc.fromEnv); got != tc.want {
			t.Errorf("parallelism(%d, %d, %v) = %q, want %q", tc.jobs, tc.scenes, tc.fromEnv, got, tc.want)
		}
	}
}

func TestSceneJobsEnvironment(t *testing.T) {
	t.Setenv(render.SceneJobsEnv, "3")
	if jobs, err := render.SceneJobs(); jobs != 3 || err != nil {
		t.Errorf("SceneJobs = %d, %v; want 3", jobs, err)
	}
	for _, bad := range []string{"0", "-2", "two"} {
		t.Setenv(render.SceneJobsEnv, bad)
		jobs, err := render.SceneJobs()
		if jobs != render.DefaultSceneJobs() || err == nil || !strings.Contains(err.Error(), render.SceneJobsEnv) {
			t.Errorf("%s=%s: SceneJobs = %d, %v; want the default and an error", render.SceneJobsEnv, bad, jobs, err)
		}
	}
}
