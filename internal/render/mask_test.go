package render

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExampleMediaShapesAndGlassPanelGraph(t *testing.T) {
	p := testV2Plan()
	media := &p.Scenes[0].Layers[0]
	media.Shape = "rounded-rectangle" // Both normal and complex templates use this shape.
	media.CornerRadius = 14
	media.AltText = "A person stretches their shoulders."
	p.Scenes[0].Layers = append(p.Scenes[0].Layers, LayerPlan{
		Kind: "text", X: 20, Y: 30, Width: 160, Height: 60, Opacity: 1,
		Effects: []EffectPlan{{Type: "glass_panel", Color: "#FFFFFF", Opacity: .72, Blur: 10, PaddingX: 20, PaddingY: 12},
			{Type: "text_shadow", Color: "#000000", Opacity: .18, OffsetX: 1, OffsetY: 2, Blur: 2}},
	})

	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}

	scene := strings.Join(commands[0], " ")
	for _, part := range []string{"perspective=", "vignette", "geq", "gblur", "split", "overlay", "drawbox"} {
		if !strings.Contains(scene, part) {
			t.Errorf("scene missing %q: %s", part, scene)
		}
	}

	if strings.Index(scene, "gblur") > strings.LastIndex(scene, "drawbox") {
		t.Error("backdrop must be blurred before panel tint")
	}

	if media.AltText != "A person stretches their shoulders." {
		t.Error("alt text was not retained")
	}

	media.Shape = "circle"
	media.CornerRadius = 0

	if _, err = p.Commands(); err != nil {
		t.Fatalf("circle: %v", err)
	}
}

func TestShapeAndGlassPanelRender(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	directory := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			pixel := color.RGBA{R: 255, A: 255}
			if x >= 32 {
				pixel = color.RGBA{B: 255, A: 255}
			}
			img.Set(x, y, pixel)
		}
	}

	imagePath := filepath.Join(directory, "image.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}

	if err = png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}

	if err = file.Close(); err != nil {
		t.Fatal(err)
	}

	for _, shape := range []string{"rounded-rectangle", "circle"} {
		t.Run(shape, func(t *testing.T) {
			work := filepath.Join(directory, shape)
			plan := &Plan{Video: VideoSpec{Width: 64, Height: 64, FPS: 10}, WorkDir: work, Output: filepath.Join(work, "out.mp4"), Duration: 200 * time.Millisecond,
				Scenes: []ScenePlan{{ID: "test", Background: "black", Duration: 200 * time.Millisecond, Output: filepath.Join(work, "scenes", "test.mkv"), Layers: []LayerPlan{
					{Kind: "image", Path: imagePath, Fit: "cover", Width: 64, Height: 64, Opacity: 1, Shape: shape, AltText: "A red and blue image", Effects: []EffectPlan{{Type: "pan_zoom", FromScale: 1, ToScale: 1.06}, {Type: "vignette", Color: "#000000", Opacity: .16}}},
					{Kind: "text", X: 16, Y: 16, Width: 32, Height: 32, Opacity: 1, Effects: []EffectPlan{{Type: "glass_panel", Color: "#FFFFFF", Opacity: .4, Blur: 2, PaddingX: 2, PaddingY: 2}}},
				}}}}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := plan.Render(ctx); err != nil {
				t.Fatalf("render %s: %v", shape, err)
			}

			frame, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", plan.Output, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
			if err != nil {
				t.Fatal(err)
			}
			if len(frame) != 64*64*3 {
				t.Fatalf("frame size = %d", len(frame))
			}

			corner := frame[:3]
			center := frame[(32*64+32)*3:][:3]
			nearEdge := frame[(32*64+30)*3:][:3]
			farFromEdge := frame[(32*64+22)*3:][:3]
			if corner[0] > 20 || corner[1] > 20 || corner[2] > 20 {
				t.Errorf("masked corner not black: %v", corner)
			}
			if center[2] < 100 {
				t.Errorf("center lost its image: %v", center)
			}
			if nearEdge[2] <= farFromEdge[2]+5 {
				t.Errorf("panel backdrop is not blurred: blue near edge %d, far from edge %d", nearEdge[2], farFromEdge[2])
			}
		})
	}
}

// renderGray renders a one-scene plan of a w×h frame and returns the luma of
// each frame of its output.
func renderGray(t *testing.T, w, h, fps int, duration time.Duration, layers []LayerPlan, tracks []TrackPlan) [][]byte {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	work := t.TempDir()
	scene := filepath.Join(work, "scenes", "test.mkv")
	// CRF 1: nearly lossless, so the checks see the render, not the encoding.
	plan := &Plan{Video: VideoSpec{Width: w, Height: h, FPS: fps, CRF: 1}, WorkDir: work, Output: filepath.Join(work, "out.mp4"), Duration: duration,
		Scenes: []ScenePlan{{ID: "test", Background: "black", Duration: duration, Output: scene, Layers: layers, Tracks: tracks}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := plan.Render(ctx); err != nil {
		t.Fatalf("render: %v", err)
	}
	raw, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", plan.Output, "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for len(raw) >= w*h {
		frames = append(frames, raw[:w*h])
		raw = raw[w*h:]
	}
	return frames
}

// writeStripes writes a w×h PNG of one-pixel black and white columns.
func writeStripes(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetGray(x, y, color.Gray{Y: uint8(255 * (x % 2))})
		}
	}
	path := filepath.Join(t.TempDir(), "stripes.png")
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

// The blurred patch is overlaid at even positions on yuv420p frames; a panel
// at odd offsets and sizes must still blur exactly its own pixels.
func TestGlassPanelBlursExactlyItsArea(t *testing.T) {
	stripes := writeStripes(t, 64, 64)
	glass := []EffectPlan{{Type: "glass_panel", Color: "#FFFFFF", Opacity: 0, Blur: 2}}
	layers := []LayerPlan{
		{Kind: "image", Path: stripes, Fit: "cover", Width: 64, Height: 64, Opacity: 1},
		{Kind: "text", X: 13, Y: 17, Width: 21, Height: 19, Opacity: 1, Effects: glass},
		// Partly and entirely outside the frame.
		{Kind: "text", X: -5, Y: 50, Width: 10, Height: 10, Opacity: 1, Effects: glass},
		{Kind: "text", X: 60, Y: -3, Width: 20, Height: 8, Opacity: 1, Effects: glass},
		{Kind: "text", X: -40, Y: 0, Width: 10, Height: 10, Opacity: 1, Effects: glass},
		{Kind: "text", X: 70, Y: 70, Width: 10, Height: 10, Opacity: 1, Effects: glass},
	}
	frame := renderGray(t, 64, 64, 10, 200*time.Millisecond, layers, nil)[0]
	sharp := func(v byte) bool { return v < 40 || v > 215 }
	at := func(x, y int) byte { return frame[y*64+x] }
	for _, p := range [][2]int{{12, 20}, {34, 20}, {20, 16}, {20, 36}} {
		if v := at(p[0], p[1]); !sharp(v) {
			t.Errorf("pixel %v outside the panel is blurred: %d", p, v)
		}
	}
	for _, p := range [][2]int{{13, 20}, {33, 20}, {20, 17}, {20, 35}, {0, 55}, {63, 2}} {
		if v := at(p[0], p[1]); sharp(v) {
			t.Errorf("pixel %v inside a panel is not blurred: %d", p, v)
		}
	}
}

// Keyframed opacity and an effect's mix are per-frame factors (frameMask);
// both must follow their keyframes frame by frame.
func TestPerFrameOpacityAndMix(t *testing.T) {
	ramp := &Animation{Props: map[string][]Keyframe{"opacity": {{At: 0, Value: 0, Ease: "linear"}, {At: time.Second, Value: 1}}}}
	luma := func(f float64) float64 { return 255 * f } // gray readback is full range
	check := func(t *testing.T, frames [][]byte, x, y int, factor func(n int) float64) {
		t.Helper()
		if len(frames) != 10 {
			t.Fatalf("frames = %d, want 10", len(frames))
		}
		for _, n := range []int{0, 3, 5, 9} {
			if got, want := float64(frames[n][y*64+x]), luma(factor(n)); math.Abs(got-want) > 4 {
				t.Errorf("frame %d at (%d,%d): luma %v, want %.0f", n, x, y, got, want)
			}
		}
	}
	t.Run("opacity", func(t *testing.T) {
		rect := ClipPlan{Kind: "rect", Duration: time.Second, Layer: LayerPlan{Kind: "rect", X: 16, Y: 16, Width: 32, Height: 32, Color: "white", Opacity: 1, Animation: ramp}}
		frames := renderGray(t, 64, 64, 10, time.Second, nil, []TrackPlan{{ID: "t", Clips: []ClipPlan{rect}}})
		check(t, frames, 32, 32, func(n int) float64 { return float64(n) / 10 })
		check(t, frames, 4, 4, func(int) float64 { return 0 })
	})
	t.Run("mix", func(t *testing.T) {
		tint := ClipPlan{Kind: "effect", Duration: time.Second, Mix: 0.5, Layer: LayerPlan{Animation: ramp},
			Adjustments: []AdjustmentPlan{{Type: "tint", Color: "white", Opacity: 1}}}
		frames := renderGray(t, 64, 64, 10, time.Second, nil, []TrackPlan{{ID: "t", Clips: []ClipPlan{tint}}})
		check(t, frames, 32, 32, func(n int) float64 { return 0.5 * float64(n) / 10 })
	})
}
