package project

import "testing"

func TestPaddingUnmarshalYAML(t *testing.T) {
	value, err := Parse([]byte(`
video:
  width: 320
  height: 240
  fps: 30
  background: black
defaults:
  text:
    background:
      color: black
      padding: 8
  subtitle:
    background:
      color: black
      padding: [12, 4]
`))
	if err != nil {
		t.Fatal(err)
	}

	if value.Defaults.Text.Background.Padding != (Padding{X: 8, Y: 8}) {
		t.Errorf("text padding = %#v, want {8, 8}", value.Defaults.Text.Background.Padding)
	}
	if value.Defaults.Subtitle.Background.Padding != (Padding{X: 12, Y: 4}) {
		t.Errorf("subtitle padding = %#v, want {12, 4}", value.Defaults.Subtitle.Background.Padding)
	}
}

func TestPaddingRejectsInvalidPairs(t *testing.T) {
	_, err := Parse([]byte(`
defaults:
  text:
    background:
      padding: [4]
`))
	if err == nil {
		t.Fatal("expected invalid padding to be rejected")
	}
}
