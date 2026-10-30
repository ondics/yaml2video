package render

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// Loudness is normalized in two passes. Render first measures the music and
// the final mix with loudnorm, then applies one fixed gain to each (and a
// limiter on the master's peaks). A fixed gain keeps the mix's dynamics:
// loudnorm's single-pass dynamic mode would ride the gain through quiet
// passages and fades, which sounds like pumping.

// musicLoudness is the level (LUFS) and true-peak ceiling (dBTP) music tracks are normalized to.
const (
	musicLoudness = -16.0
	musicTruePeak = -1.5
)

// loudnessStats are loudnorm's measurements of a stream.
type loudnessStats struct {
	I, TP float64 // integrated loudness (LUFS) and true peak (dBTP)
}

// gainTo returns the gain (dB) that brings s to target loudness without
// pushing its true peak above peak. Silence needs no gain.
func (s loudnessStats) gainTo(target, peak float64) float64 {
	if math.IsInf(s.I, 0) || math.IsNaN(s.I) {
		return 0
	}
	gain := target - s.I
	if !math.IsInf(s.TP, 0) {
		gain = math.Min(gain, peak-s.TP)
	}
	return gain
}

// measureAudio runs the analysis passes: the music before normalizing it, then
// the mix (with the music's gain applied) before the master stage.
func (p *Plan) measureAudio(ctx context.Context) error {
	if p.Music != nil && p.Music.Normalize && p.Music.GainDB == nil {
		stats, err := measureLoudness(ctx, p.musicSource(), musicLoudness, musicTruePeak)
		if err != nil {
			return fmt.Errorf("measure music loudness: %w", err)
		}
		gain := stats.gainTo(musicLoudness, musicTruePeak)
		p.Music.GainDB = &gain
	}
	if p.Master != nil && p.Master.GainDB == nil {
		mix := p.mixAudioTracks()
		if mix == nil {
			return nil
		}
		stats, err := measureLoudness(ctx, mix, p.Master.Loudness, p.Master.TruePeak)
		if err != nil {
			return fmt.Errorf("measure mix loudness: %w", err)
		}
		// The limiter catches the peaks, so the gain only follows the loudness.
		gain := stats.gainTo(p.Master.Loudness, math.Inf(1))
		p.Master.GainDB = &gain
	}
	return nil
}

// measureLoudness runs audio through loudnorm's analysis and returns its input measurements.
func measureLoudness(ctx context.Context, audio *ffmpeg.Stream, target, peak float64) (loudnessStats, error) {
	analysis := audio.Filter("loudnorm", nil, ffmpeg.KwArgs{"I": target, "TP": peak, "LRA": 11, "print_format": "json"})
	args := ffmpeg.Output([]*ffmpeg.Stream{analysis}, "-", ffmpeg.KwArgs{"f": "null"}).Compile().Args
	cmd := exec.CommandContext(ctx, args[0], append([]string{"-hide_banner", "-nostats"}, args[1:]...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return loudnessStats{}, fmt.Errorf("%w: %s", err, tail(string(output), 600))
	}
	return parseLoudnorm(string(output))
}

// parseLoudnorm reads the JSON block loudnorm prints at the end of its log.
func parseLoudnorm(log string) (loudnessStats, error) {
	start, end := strings.LastIndex(log, "{"), strings.LastIndex(log, "}")
	if start < 0 || end < start {
		return loudnessStats{}, fmt.Errorf("no loudnorm measurements in ffmpeg output")
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(log[start:end+1]), &raw); err != nil {
		return loudnessStats{}, fmt.Errorf("loudnorm measurements: %w", err)
	}
	var stats loudnessStats
	for key, dst := range map[string]*float64{"input_i": &stats.I, "input_tp": &stats.TP} {
		value, err := strconv.ParseFloat(raw[key], 64)
		if err != nil {
			return loudnessStats{}, fmt.Errorf("loudnorm %s: %w", key, err)
		}
		*dst = value
	}
	return stats, nil
}

// masterStage brings the mix to the master loudness: the measured gain and a
// peak limiter, or loudnorm's dynamic mode when nothing was measured.
func (p *Plan) masterStage(mix *ffmpeg.Stream) *ffmpeg.Stream {
	m := p.Master
	if m.GainDB == nil {
		// loudnorm resamples internally, so return to 48 kHz afterwards.
		return mix.Filter("loudnorm", nil, ffmpeg.KwArgs{"I": m.Loudness, "TP": m.TruePeak, "LRA": 11}).
			Filter("aresample", ffmpeg.Args{"48000"})
	}
	// alimiter's level option would add automatic gain of its own; it stays off.
	return mix.Filter("volume", nil, ffmpeg.KwArgs{"volume": fmt.Sprintf("%.2fdB", *m.GainDB)}).
		Filter("alimiter", nil, ffmpeg.KwArgs{"limit": num6(math.Pow(10, m.TruePeak/20)), "attack": 5, "release": 80, "level": 0})
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
