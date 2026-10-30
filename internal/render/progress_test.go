package render

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseFFmpegProgress(t *testing.T) {
	input := "frame=1\nout_time_us=N/A\nout_time_ms=1234\nout_time_us=-1\n" +
		"out_time_us=bad\nout_time_us=9223372036854775808\nout_time_us=500000\r\n" +
		"progress=continue\nout_time_us=1250000\nprogress=end\n"
	var got []float64
	if err := parseFFmpegProgress(strings.NewReader(input), func(seconds float64) {
		got = append(got, seconds)
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []float64{0.5, 1.25}) {
		t.Fatalf("timestamps = %v", got)
	}
	if err := parseFFmpegProgress(strings.NewReader(strings.Repeat("x", 100000)), nil); err == nil {
		t.Fatal("expected scanner error for an oversized line")
	}
}

func fakeFFmpeg(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ffmpeg uses a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func progressTestPlan(t *testing.T) *Plan {
	t.Helper()
	dir := t.TempDir()
	return &Plan{
		Video: VideoSpec{Width: 16, Height: 16, FPS: 10},
		Scenes: []ScenePlan{
			{ID: "one", Duration: time.Second, Background: "black", Output: filepath.Join(dir, "one.mkv")},
			{ID: "two", Duration: 3 * time.Second, Background: "black", Output: filepath.Join(dir, "two.mkv")},
		},
		Transitions: []BoundaryTransition{{FromScene: 0, Type: "fade", Duration: time.Second}},
		Duration:    3 * time.Second,
		Output:      filepath.Join(dir, "final.mp4"), WorkDir: dir,
	}
}

func TestRenderWithProgressWeighted(t *testing.T) {
	fakeFFmpeg(t, "printf 'out_time_us=500000\nout_time_us=100000\nout_time_us=100000000\nprogress=end\n'\n")
	plan := progressTestPlan(t)
	commands, err := plan.Commands()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range commands {
		if !reflect.DeepEqual(args[:5], []string{"ffmpeg", "-nostdin", "-progress", "pipe:1", "-nostats"}) {
			t.Fatalf("missing progress arguments: %v", args)
		}
	}
	var events []Progress
	if err := plan.RenderWithProgress(context.Background(), func(p Progress) { events = append(events, p) }); err != nil {
		t.Fatal(err)
	}
	first := events[0]
	last := events[len(events)-1]
	if first != (Progress{Stage: "scene", Total: 3}) || last != (Progress{Stage: "done", Percent: 100, Completed: 3, Total: 3}) {
		t.Fatalf("first/last = %+v / %+v", first, last)
	}
	for i, event := range events {
		if event.Percent < 0 || event.Percent > 100 || (i > 0 && event.Percent < events[i-1].Percent) {
			t.Fatalf("non-monotonic/out-of-range event: %+v", events)
		}
		if i < len(events)-1 && event.Percent == 100 {
			t.Fatal("100 emitted before successful completion")
		}
	}
	// Five events per command: start, three timestamps, successful completion.
	for _, tc := range []struct {
		index   int
		percent float64
	}{
		{1, 100 * 0.5 / 7}, {2, 100 * 0.5 / 7}, {3, 100.0 / 7},
		{6, 100 * 1.5 / 7}, {11, 100 * 4.5 / 7},
	} {
		if math.Abs(events[tc.index].Percent-tc.percent) > 1e-9 {
			t.Errorf("event %d = %g, want %g", tc.index, events[tc.index].Percent, tc.percent)
		}
	}
	if events[10].Stage != "final" || events[10].Completed != 2 {
		t.Fatalf("final event = %+v", events[10])
	}
	raw, err := json.Marshal(last)
	if err != nil || string(raw) != `{"stage":"done","percent":100,"completed":3,"total":3}` {
		t.Fatalf("JSON = %s, err = %v", raw, err)
	}
}

func TestRenderCancellation(t *testing.T) {
	fakeFFmpeg(t, "printf 'out_time_us=500000\n'\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var events []Progress
	start := time.Now()
	err := progressTestPlan(t).RenderWithProgress(ctx, func(p Progress) {
		events = append(events, p)
		if p.Percent > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation during FFmpeg, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation did not stop FFmpeg promptly")
	}
	for _, event := range events {
		if event.Percent == 100 || event.Completed != 0 {
			t.Fatalf("canceled command counted as completed: %+v", event)
		}
	}
	if err := progressTestPlan(t).Render(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Render wrapper ignores pre-canceled context: %v", err)
	}
}

func TestRenderFailureAndBoundedStderr(t *testing.T) {
	fakeFFmpeg(t, "printf 'out_time_us=999999999\nprogress=end\n'\nprintf 'render failed' >&2\nexit 1\n")
	var events []Progress
	err := progressTestPlan(t).RenderWithProgress(context.Background(), func(p Progress) { events = append(events, p) })
	if err == nil || !strings.Contains(err.Error(), "render failed") {
		t.Fatalf("missing FFmpeg stderr: %v", err)
	}
	for _, event := range events {
		if event.Percent == 100 || event.Completed != 0 {
			t.Fatalf("failed command counted as completed: %+v", event)
		}
	}
	b := &stderrTail{}
	for _, data := range []string{strings.Repeat("a", stderrTailLimit+100), "suffix", strings.Repeat("b", 100)} {
		if n, err := b.Write([]byte(data)); n != len(data) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if len(b.data) != stderrTailLimit || !strings.HasSuffix(string(b.data), "suffix"+strings.Repeat("b", 100)) {
		t.Fatal("stderr tail is not bounded or did not preserve latest output")
	}
}

func TestRenderWithRealFFmpegProgress(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	plan := progressTestPlan(t)
	plan.Scenes = plan.Scenes[:1]
	plan.Transitions = nil
	plan.Duration = plan.Scenes[0].Duration
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var timestampProgress bool
	var last Progress
	if err := plan.RenderWithProgress(ctx, func(p Progress) {
		if p.Completed == 0 && p.Percent > 0 {
			timestampProgress = true
		}
		last = p
	}); err != nil {
		t.Fatal(err)
	}
	if !timestampProgress || last.Stage != "done" || last.Percent != 100 {
		t.Fatalf("real timestamp progress = %v, last = %+v", timestampProgress, last)
	}
}
