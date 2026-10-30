package render

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// Progress describes duration-weighted render progress. Stage is "scene",
// "final", or "done". Completed and Total count FFmpeg commands (one per scene
// plus the final pass). Percent is monotonic in [0, 100]; only success emits 100.
// This is media-duration progress, not an estimate of remaining wall-clock time.
type Progress struct {
	Stage     string  `json:"stage"`
	Percent   float64 `json:"percent"`
	Completed int     `json:"completed"`
	Total     int     `json:"total"`
}

type renderProgress struct {
	weights  []float64
	total    float64
	percent  float64
	callback func(Progress)
}

func newRenderProgress(weights []float64, callback func(Progress)) *renderProgress {
	p := &renderProgress{weights: weights, callback: callback}
	for _, weight := range weights {
		p.total += weight
	}
	return p
}

func (p *renderProgress) report(stage string, completed int, seconds float64) {
	work := 0.0
	for _, weight := range p.weights[:completed] {
		work += weight
	}
	if completed < len(p.weights) {
		work += min(max(seconds, 0), p.weights[completed])
	}
	percent := 0.0
	if p.total > 0 {
		// Keep 100 exclusive to the successful "done" event, even when
		// FFmpeg reports the full duration before exiting with an error.
		percent = min(100*work/p.total, math.Nextafter(100, 0))
	}
	if stage == "done" {
		percent = 100
	}
	p.percent = max(p.percent, percent)
	if p.callback != nil {
		p.callback(Progress{Stage: stage, Percent: p.percent, Completed: completed, Total: len(p.weights)})
	}
}

// parseFFmpegProgress ignores other keys and unavailable/malformed timestamps.
// Parse as seconds without multiplying an untrusted integer into a Duration.
func parseFFmpegProgress(r io.Reader, callback func(float64)) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || key != "out_time_us" {
			continue
		}
		us, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && us >= 0 && callback != nil {
			callback(float64(us) / 1e6)
		}
	}
	return scanner.Err()
}

func runFFmpeg(ctx context.Context, args []string, callback func(float64)) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stderr := &stderrTail{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	parseErr := parseFFmpegProgress(stdout, callback)
	if parseErr != nil {
		// Stop the producer if malformed output exceeds the scanner limit.
		// Otherwise Wait could block on a full, no-longer-drained stdout pipe.
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return fmt.Errorf("ffmpeg: %w", ctx.Err())
	}
	if parseErr != nil {
		return fmt.Errorf("read ffmpeg progress: %w: %s", parseErr, stderr.data)
	}
	if waitErr != nil {
		return fmt.Errorf("ffmpeg: %w: %s", waitErr, stderr.data)
	}
	return nil
}

const stderrTailLimit = 16 * 1024

// stderrTail retains only the last bytes; exec writes it from a single goroutine
// and callers read it only after Wait has joined that goroutine.
type stderrTail struct {
	data []byte
}

func (b *stderrTail) Write(data []byte) (int, error) {
	n := len(data)
	if n >= stderrTailLimit {
		b.data = append(b.data[:0], data[n-stderrTailLimit:]...)
	} else {
		if excess := len(b.data) + n - stderrTailLimit; excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, data...)
	}
	return n, nil
}
