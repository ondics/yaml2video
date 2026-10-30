package render

import (
	"context"
	"image"
	"image/color"
	"image/png"
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
	for _, part := range []string{"zoompan", "vignette", "geq", "gblur", "split", "overlay", "drawbox"} {
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
