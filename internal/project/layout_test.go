package project

import "testing"

func TestTrimUnmarshalYAML(t *testing.T) {
	value, err := Parse([]byte(`
video:
  width: 320
  height: 240
  fps: 30
  background: black
scenes:
  - id: trims
    duration: 1
    layers:
      - type: video
        path: source.mp4
        trim: 0.1
      - type: video
        path: source.mp4
        trim: [0.2, 0.1]
      - type: video
        path: source.mp4
        trim: [0.1, 0.2, 0.3, 0.1]
`))
	if err != nil {
		t.Fatal(err)
	}

	trims := []Trim{
		{Top: 0.1, Bottom: 0.1, Left: 0.1, Right: 0.1},
		{Top: 0.2, Bottom: 0.2, Left: 0.1, Right: 0.1},
		{Top: 0.1, Bottom: 0.2, Left: 0.3, Right: 0.1},
	}

	for index, expected := range trims {
		actual := *value.Scenes[0].Layers[index].Trim
		if actual != expected {
			t.Errorf("trim %d = %#v, want %#v", index, actual, expected)
		}
	}
}

func TestTrimRejectsEmptySourceFrame(t *testing.T) {
	_, err := Parse([]byte(`
scenes:
  - id: invalid
    duration: 1
    layers:
      - type: video
        path: source.mp4
        trim: [0.5, 0.5]
`))
	if err == nil {
		t.Fatal("expected trim that removes the entire frame to be rejected")
	}
}
