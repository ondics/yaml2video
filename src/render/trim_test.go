package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ondics/yaml2video/src/project"
)

func TestCompileMediaTrim(t *testing.T) {
	for _, kind := range []string{"image", "video"} {
		for _, fit := range []string{"contain", "cover"} {
			for _, tc := range []struct {
				name string
				yaml string
				want *TrimPlan
			}{
				{name: "omitted"},
				{name: "zero", yaml: "trim: 0", want: &TrimPlan{}},
				{name: "uniform", yaml: "trim: 0.1", want: &TrimPlan{Top: .1, Bottom: .1, Left: .1, Right: .1}},
				{name: "axes", yaml: "trim: [0.1, 0.2]", want: &TrimPlan{Top: .1, Bottom: .1, Left: .2, Right: .2}},
				{name: "edges", yaml: "trim: [0.1, 0.2, 0.3, 0.05]", want: &TrimPlan{Top: .1, Bottom: .2, Left: .3, Right: .05}},
			} {
				t.Run(kind+"/"+fit+"/"+tc.name, func(t *testing.T) {
					value, err := project.Parse(fmt.Appendf(nil, `
video:
  width: 320
  height: 240
  fps: 30
  background: black
scenes:
  - id: trim
    duration: 1
    layers:
      - type: %s
        path: source.png
        width: 100
        height: 80
        fit: %s
        %s
`, kind, fit, tc.yaml))
					if err != nil {
						t.Fatal(err)
					}
					plan, err := Compile(value, Options{})
					if err != nil {
						t.Fatal(err)
					}
					got := plan.Scenes[0].Layers[0].Trim
					if tc.want == nil {
						if got != nil {
							t.Fatalf("unexpected trim: %#v", got)
						}
					} else if got == nil || *got != *tc.want {
						t.Fatalf("trim = %#v, want %#v", got, tc.want)
					}

					commands, err := plan.Commands()
					if err != nil {
						t.Fatal(err)
					}
					command := strings.Join(commands[0], " ")
					if tc.want == nil {
						if strings.Contains(command, "crop=iw*") {
							t.Errorf("untrimmed source has a trim filter: %s", command)
						}
						return
					}
					crop := fmt.Sprintf("crop=iw*(1-%g-%g):ih*(1-%g-%g):iw*%g:ih*%g",
						tc.want.Left, tc.want.Right, tc.want.Top, tc.want.Bottom, tc.want.Left, tc.want.Top)
					cropIndex := strings.Index(command, crop)
					scaleIndex := strings.Index(command, "scale=100:80")
					if cropIndex < 0 || scaleIndex < cropIndex {
						t.Errorf("expected %q before scaling: %s", crop, command)
					}
				})
			}
		}
	}
}

func TestCompileMediaRejectsInvalidTrim(t *testing.T) {
	for _, kind := range []project.LayerType{project.LayerTypeImage, project.LayerTypeVideo} {
		for _, trim := range []project.Trim{
			{Top: -.1},
			{Top: .5, Bottom: .5},
			{Left: .6, Right: .5},
		} {
			layer := project.Layer{Type: kind, LayerMedia: project.LayerMedia{Path: "source.png", Trim: &trim}}
			_, err := compileMediaLayer(layer, LayerPlan{})
			if err == nil || !strings.Contains(err.Error(), string(kind)+" trim:") {
				t.Errorf("%s trim %#v: expected a trim validation error, got %v", kind, trim, err)
			}
		}
	}
}

func TestImageTrimRender(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	directory := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			pixel := color.RGBA{R: 255, A: 255}
			if x >= 24 && x < 56 && y >= 8 && y < 48 {
				pixel = color.RGBA{G: 255, A: 255}
			}
			img.SetRGBA(x, y, pixel)
		}
	}
	imagePath := filepath.Join(directory, "border.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	for _, fit := range []string{"contain", "cover"} {
		t.Run(fit, func(t *testing.T) {
			work := filepath.Join(directory, fit)
			plan := &Plan{
				Video: VideoSpec{Width: 64, Height: 80, FPS: 10}, WorkDir: work,
				Output: filepath.Join(work, "out.mp4"), Duration: 200 * time.Millisecond,
				Scenes: []ScenePlan{{
					ID: "trim", Background: "black", Duration: 200 * time.Millisecond,
					Output: filepath.Join(work, "scenes", "trim.mkv"),
					Layers: []LayerPlan{{
						Kind: "image", Path: imagePath, Fit: fit, Width: 64, Height: 80, Opacity: 1,
						Trim: &TrimPlan{Top: .125, Bottom: .25, Left: .375, Right: .125},
					}},
				}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := plan.Render(ctx); err != nil {
				t.Fatal(err)
			}
			frame, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", plan.Output,
				"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
			if err != nil {
				t.Fatal(err)
			}
			if len(frame) != 64*80*3 {
				t.Fatalf("frame size = %d, want %d", len(frame), 64*80*3)
			}
			for _, point := range []image.Point{{2, 2}, {61, 2}, {2, 77}, {61, 77}, {32, 40}} {
				pixel := frame[(point.Y*64+point.X)*3:][:3]
				if pixel[0] > 20 || pixel[1] < 230 || pixel[2] > 20 {
					t.Errorf("pixel at %v = %v, want green after trimming red borders", point, pixel)
				}
			}
		})
	}
}
