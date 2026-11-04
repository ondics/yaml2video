package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageBackgrounds(t *testing.T) {
	const background = `{ path: "$(metadata.title).png", fit: contain, color: "#123456", right: 0.25, bottom: 0 }`
	base, _, _ := strings.Cut(folderTemplate, "  card:\n")
	video := strings.ReplaceAll(folderVideo, "{ id:", "{ image: folders.png, text: hello, id:")
	for _, tc := range []struct{ name, layout string }{
		{"theme", `[]`},
		{"custom", `{ background: ` + background + `, elements: [] }`},
		{"classic", `{ background: ` + background + `, image: { placement: { anchor: center } }, text: { placement: { anchor: center } } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			image := filepath.Join(dir, "folders.png")
			if err := os.WriteFile(image, []byte("stub"), 0o600); err != nil {
				t.Fatal(err)
			}
			template := base + "  card: " + tc.layout + "\n"
			if tc.name == "theme" {
				template = strings.Replace(template, `background: "#000000"`, "background: "+background, 1)
			}
			p, err := FromBytes([]byte(video), []byte(template), dir)
			if err != nil {
				t.Fatal(err)
			}
			scene := p.Scenes[0]
			if scene.Background != "#123456" || len(scene.Layers) == 0 {
				t.Fatalf("background not composed: %+v", scene)
			}
			layer := scene.Layers[0]
			if layer.Path != image || layer.Fit != "contain" || layer.X != 0 || layer.Y != 0 || layer.Width != p.Video.Width || layer.Height != p.Video.Height {
				t.Fatalf("background must be a full-frame image beneath the layout: %+v", layer)
			}
			commands, err := p.Commands()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(commands[0], " "), "pad=1080:1920:(ow-iw)*0.75:(oh-ih)*1:color=black@0") {
				t.Fatal("background did not use the shared positioned media fit")
			}
		})
	}
}

func TestMediaFitPosition(t *testing.T) {
	for _, tc := range []struct {
		name, fit, want string
		position        obj
	}{
		{"left-top", "cover", "crop=200:100:(iw-ow)*0:(ih-oh)*0", obj{"left": 0.0, "top": 0.0}},
		{"center", "cover", "crop=200:100:(iw-ow)*0.5:(ih-oh)/2", obj{"left": 0.5}},
		{"right-bottom", "cover", "crop=200:100:(iw-ow)*1:(ih-oh)*0.75", obj{"right": 0.0, "bottom": 0.25}},
		{"contain", "contain", "pad=200:100:(ow-iw)*0.25:(oh-ih)*1:color=black@0", obj{"left": 0.25, "top": 1.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testV2Plan()
			el := merge(tc.position, obj{"fit": tc.fit, "placement": obj{"anchor": "center"}, "max_width_ratio": 0.625, "max_height_ratio": 100.0 / 240})
			layer, err := mediaLayer("photo.png", el, obj{}, p.Video)
			if err != nil {
				t.Fatal(err)
			}
			if layer.X != 60 || layer.Y != 70 {
				t.Fatalf("fit position moved the box: (%d, %d)", layer.X, layer.Y)
			}
			p.Scenes[0].Layers = []LayerPlan{layer}
			commands, err := p.Commands()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(commands[0], " "), tc.want) {
				t.Fatalf("missing fit expression %s", tc.want)
			}
		})
	}
	for _, position := range []obj{{"left": -0.1}, {"top": 1.1}, {"left": 0.0, "right": 0.0}, {"top": 0.5, "bottom": 0.5}} {
		if _, err := mediaLayer("photo.png", position, obj{}, VideoSpec{Width: 320, Height: 240}); err == nil {
			t.Errorf("accepted invalid fit position: %v", position)
		}
	}
}
