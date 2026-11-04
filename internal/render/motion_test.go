package render

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTexture writes a w×h PNG of fine detail, where any stepping shows.
func writeTexture(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetGray(x, y, color.Gray{Y: uint8(128 + 120*math.Sin(float64(x)*0.9)*math.Cos(float64(y)*0.7))})
		}
	}
	path := filepath.Join(t.TempDir(), "texture.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// checkSmooth fails when a steady motion inside the region steps: frames that
// repeat the previous one, uneven frame-to-frame change, or detail that
// pulses as the subpixel offset changes.
func checkSmooth(t *testing.T, frames [][]byte, w int, region image.Rectangle) {
	t.Helper()
	var change, detail []float64
	for i, f := range frames {
		sum, sumSq, diff := 0.0, 0.0, 0.0
		for y := region.Min.Y; y < region.Max.Y; y++ {
			for x := region.Min.X; x < region.Max.X; x++ {
				v := float64(f[y*w+x])
				sum, sumSq = sum+v, sumSq+v*v
				if i > 0 {
					diff += math.Abs(v - float64(frames[i-1][y*w+x]))
				}
			}
		}
		n := float64(region.Dx() * region.Dy())
		detail = append(detail, math.Sqrt(sumSq/n-(sum/n)*(sum/n)))
		if i > 0 {
			change = append(change, diff/n)
		}
	}
	mean := 0.0
	for _, c := range change {
		mean += c
	}
	mean /= float64(len(change))
	for i, c := range change {
		if c < mean/2 || c > mean*1.5 {
			t.Fatalf("frame %d changes by %.2f, the motion averages %.2f per frame: %.2f", i+1, c, mean, change)
		}
	}
	lo, hi := detail[0], detail[0]
	for _, d := range detail {
		lo, hi = math.Min(lo, d), math.Max(hi, d)
	}
	if (hi-lo)/hi > 0.06 {
		t.Errorf("detail pulses between %.1f and %.1f", lo, hi)
	}
}

// A slow keyframed move (0.15 px per frame) glides instead of stepping
// whole pixels, and a still image's frames keep exact times.
func TestSlowMoveIsSmooth(t *testing.T) {
	move := &Animation{Props: map[string][]Keyframe{"x": {{At: 0, Value: 0, Ease: "linear"}, {At: 2 * time.Second, Value: 9}}}}
	clip := ClipPlan{Kind: "image", Duration: 2 * time.Second, Layer: LayerPlan{Kind: "image", Path: writeTexture(t, 64, 64), Fit: "cover",
		X: 40, Y: 40, Width: 64, Height: 64, Opacity: 1, Animation: move}}
	frames := renderGray(t, 160, 160, 30, 2*time.Second, nil, []TrackPlan{{ID: "t", Clips: []ClipPlan{clip}}})
	checkSmooth(t, frames, 160, image.Rect(60, 52, 96, 92))
}

// A slow pan_zoom zooms continuously rather than in whole-pixel crops.
func TestSlowPanZoomIsSmooth(t *testing.T) {
	layer := LayerPlan{Kind: "image", Path: writeTexture(t, 128, 128), Fit: "cover", X: 16, Y: 16, Width: 128, Height: 128, Opacity: 1,
		Effects: []EffectPlan{{Type: "pan_zoom", FromScale: 1, ToScale: 1.04, FromAnchor: "center", ToAnchor: "center"}}}
	frames := renderGray(t, 160, 160, 30, 2*time.Second, []LayerPlan{layer}, nil)
	checkSmooth(t, frames, 160, image.Rect(20, 20, 60, 60))
}

// Frame n shows the keyframes at n/fps: perspective counts its frames from 1.
func TestKeyframedMoveIsOnTime(t *testing.T) {
	move := &Animation{Props: map[string][]Keyframe{"x": {{At: 0, Value: 0, Ease: "linear"}, {At: time.Second, Value: 30}}}}
	bar := ClipPlan{Kind: "rect", Duration: time.Second, Layer: LayerPlan{Kind: "rect", X: 10, Y: 10, Width: 4, Height: 40, Color: "white", Opacity: 1, Animation: move}}
	frames := renderGray(t, 64, 64, 30, time.Second, nil, []TrackPlan{{ID: "t", Clips: []ClipPlan{bar}}})
	for _, n := range []int{0, 10, 29} {
		row := frames[n][30*64:]
		if left := 10 + n; row[left-1] > 40 || row[left] < 215 {
			t.Errorf("frame %d: bar should start at x=%d: %v", n, left, row[left-2:left+2])
		}
	}
}

// An effect clip renders only its window of frames; its camera keyframes
// still follow the clip's own time, frame for frame.
func TestEffectWindowIsOnTime(t *testing.T) {
	bar := ClipPlan{Kind: "rect", Duration: 2 * time.Second, Layer: LayerPlan{Kind: "rect", X: 10, Y: 0, Width: 4, Height: 64, Color: "white", Opacity: 1}}
	pan := &Animation{Props: map[string][]Keyframe{"x": {{At: 0, Value: 0, Ease: "linear"}, {At: time.Second, Value: 30}}}}
	camera := ClipPlan{Kind: "effect", Start: time.Second, Duration: time.Second, Mix: 1, Layer: LayerPlan{Animation: pan},
		Adjustments: []AdjustmentPlan{{Type: "rgb_split", Amount: 0}}}
	frames := renderGray(t, 64, 64, 30, 2*time.Second, nil, []TrackPlan{{ID: "bar", Clips: []ClipPlan{bar}}, {ID: "camera", Clips: []ClipPlan{camera}}})
	for n, left := range map[int]int{29: 10, 30: 10, 40: 20, 59: 39} {
		row := frames[n][32*64:]
		if row[left-1] > 40 || row[left] < 215 {
			t.Errorf("frame %d: bar should start at x=%d: %v", n, left, row[left-2:left+2])
		}
	}
}
