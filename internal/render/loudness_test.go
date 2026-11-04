package render

import (
	"context"
	"math"
	"os/exec"
	"strings"
	"testing"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const loudnormLog = `[Parsed_loudnorm_0 @ 0x1]
{
	"input_i" : "-11.80",
	"input_tp" : "-0.40",
	"input_lra" : "6.10",
	"input_thresh" : "-22.03",
	"output_i" : "-14.10",
	"output_tp" : "-2.00",
	"output_lra" : "5.00",
	"output_thresh" : "-24.20",
	"normalization_type" : "dynamic",
	"target_offset" : "0.10"
}
`

func TestParseLoudnorm(t *testing.T) {
	stats, err := parseLoudnorm("size=N/A time=00:00:36.50\n" + loudnormLog)
	if err != nil || stats.I != -11.8 || stats.TP != -0.4 {
		t.Fatalf("stats %+v, %v", stats, err)
	}
	if _, err := parseLoudnorm("no json here"); err == nil {
		t.Error("want an error without measurements")
	}
	silent, err := parseLoudnorm(strings.NewReplacer(`"-11.80"`, `"-inf"`, `"-0.40"`, `"-inf"`).Replace(loudnormLog))
	if err != nil || silent.gainTo(-14, -1) != 0 {
		t.Errorf("silence: %+v, %v", silent, err)
	}
}

func TestGainTo(t *testing.T) {
	stats := loudnessStats{I: -20, TP: -6}
	if got := stats.gainTo(-14, -1); got != 5 { // +6 would push the peak to 0 dBTP
		t.Errorf("gain limited by the peak = %g, want 5", got)
	}
	if got := stats.gainTo(-14, math.Inf(1)); got != 6 {
		t.Errorf("gain = %g, want 6", got)
	}
}

func TestMasterStageUsesMeasuredGain(t *testing.T) {
	mix := ffmpeg.Input("in.wav").Audio()
	p := &Plan{Master: &MasterPlan{Loudness: -14, TruePeak: -1.5}}
	args := strings.Join(ffmpeg.Output([]*ffmpeg.Stream{p.masterStage(mix)}, "out.wav").Compile().Args, " ")
	if !strings.Contains(args, "loudnorm") {
		t.Errorf("unmeasured master should fall back to loudnorm: %s", args)
	}
	gain := -2.25
	p.Master.GainDB = &gain
	args = strings.Join(ffmpeg.Output([]*ffmpeg.Stream{p.masterStage(mix)}, "out.wav").Compile().Args, " ")
	if strings.Contains(args, "loudnorm") || !strings.Contains(args, "volume=volume=-2.25dB") || !strings.Contains(args, "alimiter") || !strings.Contains(args, "level=0") {
		t.Errorf("measured master: %s", args)
	}
}

func TestMeasureLoudnessWithFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tone := ffmpeg.Input("sine=frequency=440:duration=2", ffmpeg.KwArgs{"f": "lavfi"}).Audio()
	stats, err := measureLoudness(ctx, tone, -14, -1)
	if err != nil {
		t.Fatal(err)
	}
	// A full-scale-ish sine (FFmpeg's default amplitude 1/8) measures around -21 LUFS.
	if stats.I > -15 || stats.I < -30 || stats.TP > 0 {
		t.Errorf("unexpected measurement %+v", stats)
	}
}

func TestSourceLengthNeverAddsAFrame(t *testing.T) {
	// 0.5 s at 30 fps is 15 frames; a source even 1 µs longer has 16.
	d := sourceLength(500*time.Millisecond, 3)
	if frames := int(math.Ceil(d.Seconds() * 30)); frames != 15 {
		t.Errorf("source of %s has %d frames, want 15", d, frames)
	}
}

func TestASSDisablesWrapping(t *testing.T) {
	var b strings.Builder
	writeV2ASSHeader(&b, VideoSpec{Width: 1080, Height: 1920}, true)
	if !strings.Contains(b.String(), "WrapStyle: 2") {
		t.Errorf("header:\n%s", b.String())
	}
}
