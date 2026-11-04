package render

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A segmented render places scenes, cuts, and blends on the right frames,
// draws the video's own tracks into every piece, and joins the pieces
// without losing or repeating frames.
func TestSegmentedRender(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	work := t.TempDir()
	scene := func(id, color string) ScenePlan {
		return ScenePlan{ID: id, Background: color, Duration: 2 * time.Second, Output: filepath.Join(work, "scenes", id+".mkv")}
	}
	// red | cut | blue | 0.5 s fade | green: 2 + 2 + 2 - 0.5 = 5.5 s, 55 frames.
	duration := 5500 * time.Millisecond
	move := &Animation{Props: map[string][]Keyframe{"x": {{At: 0, Value: 0, Ease: "linear"}, {At: 5400 * time.Millisecond, Value: 54}}}}
	plan := &Plan{
		Video: VideoSpec{Width: 64, Height: 64, FPS: 10, CRF: 1}, WorkDir: work, Output: filepath.Join(work, "out.mp4"), Duration: duration,
		Scenes:      []ScenePlan{scene("red", "red"), scene("blue", "blue"), scene("green", "green")},
		Transitions: []BoundaryTransition{{FromScene: 1, Type: "fade", Duration: 500 * time.Millisecond}},
		Tracks: []TrackPlan{{ID: "video", Clips: []ClipPlan{
			{Kind: "box", Duration: duration, Layer: LayerPlan{Width: 4, Height: 4, Color: "white", Opacity: 1}},
			{Kind: "rect", Duration: duration, Layer: LayerPlan{Kind: "rect", X: 4, Y: 40, Width: 4, Height: 10, Color: "white", Opacity: 1, Animation: move}},
		}}},
	}
	if _, ok := plan.segments(); !ok {
		t.Fatal("plan should render in pieces")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := plan.Render(ctx); err != nil {
		t.Fatal(err)
	}

	raw, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", plan.Output, "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 * 64 * 3
	if len(raw) != 55*size {
		t.Fatalf("frames = %d, want 55", len(raw)/size)
	}
	pixel := func(n, x, y int) [3]byte {
		at := n*size + (y*64+x)*3
		return [3]byte{raw[at], raw[at+1], raw[at+2]}
	}
	is := func(c [3]byte, r, g, b bool) bool {
		on := func(v byte, want bool) bool { return want == (v > 100) }
		return on(c[0], r) && on(c[1], g) && on(c[2], b)
	}
	for n, want := range map[int][3]bool{0: {true, false, false}, 19: {true, false, false}, 20: {false, false, true}, 34: {false, false, true}, 40: {false, true, false}, 54: {false, true, false}} {
		if c := pixel(n, 32, 20); !is(c, want[0], want[1], want[2]) {
			t.Errorf("frame %d shows %v, want %v", n, c, want)
		}
	}
	// green (#008000) ramps up 0, 0.2, ... 0.8 over the blended frames 35–39.
	if blend := pixel(37, 32, 20); blend[1] < 30 || blend[2] < 100 {
		t.Errorf("frame 37 is not a blend of blue and green: %v", blend)
	}
	for _, n := range []int{0, 19, 20, 37, 54} {
		if c := pixel(n, 1, 1); !is(c, true, true, true) {
			t.Errorf("frame %d lacks the video's box: %v", n, c)
		}
		// The moving rect covers x 4+n … 7+n.
		if on, off := pixel(n, 5+n, 45), pixel(n, 2+n, 45); !is(on, true, true, true) || is(off, true, true, true) {
			t.Errorf("frame %d: moving rect off its keyframes: at %d %v, at %d %v", n, 5+n, on, 2+n, off)
		}
	}

	tags, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "stream=color_range,color_space", "-of", "csv=p=0", plan.Output).Output()
	if err != nil || strings.TrimSpace(string(tags)) != "tv,smpte170m" {
		t.Errorf("color tags = %q, %v", tags, err)
	}
}

// A scene shorter than the blends at both its ends renders in one final pass.
func TestShortScenesRenderInOnePass(t *testing.T) {
	p := progressTestPlan(t)
	middle := p.Scenes[0]
	middle.ID, middle.Duration, middle.Output = "middle", 1500*time.Millisecond, filepath.Join(p.WorkDir, "middle.mkv")
	p.Scenes = []ScenePlan{p.Scenes[1], middle, p.Scenes[1]}
	p.Scenes[2].Output = filepath.Join(p.WorkDir, "three.mkv")
	p.Transitions = []BoundaryTransition{{FromScene: 0, Type: "fade", Duration: time.Second}, {FromScene: 1, Type: "fade", Duration: time.Second}}
	if _, ok := p.segments(); ok {
		t.Fatal("a scene shorter than its two blends cannot render in pieces")
	}
	commands, err := p.Commands()
	if err != nil {
		t.Fatal(err)
	}
	final := strings.Join(commands[len(commands)-1], " ")
	if len(commands) != 4 || strings.Count(final, "xfade") != 2 || !strings.Contains(final, "libx264") {
		t.Errorf("one-pass commands: %v", commands)
	}
}
