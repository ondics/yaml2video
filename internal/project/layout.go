package project

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// LayerLayout defines optional pixel dimensions shared by all layer types.
type LayerLayout struct {
	Width  *int `yaml:"width"`
	Height *int `yaml:"height"`
}

// Trim describes fractional insets removed from an image or video's source frame.
type Trim struct {
	Top    float64
	Bottom float64
	Left   float64
	Right  float64
}

// UnmarshalYAML accepts `trim: 0.1`, `trim: [vertical, horizontal]`, or
// `trim: [top, bottom, left, right]`.
func (t *Trim) UnmarshalYAML(node ast.Node) error {
	var uniform float64
	if err := yaml.NodeToValue(node, &uniform); err == nil {
		t.Top = uniform
		t.Bottom = uniform
		t.Left = uniform
		t.Right = uniform
		return t.Validate()
	}

	var values []float64
	if err := yaml.NodeToValue(node, &values); err != nil {
		return fmt.Errorf("trim must be a fraction or a [vertical, horizontal] or [top, bottom, left, right] list")
	}

	switch len(values) {
	case 2:
		t.Top = values[0]
		t.Bottom = values[0]
		t.Left = values[1]
		t.Right = values[1]
	case 4:
		t.Top = values[0]
		t.Bottom = values[1]
		t.Left = values[2]
		t.Right = values[3]
	default:
		return fmt.Errorf("trim list must contain two or four values")
	}

	return t.Validate()
}

// Validate ensures trimming leaves a non-empty source frame.
func (t Trim) Validate() error {
	if t.Top < 0 || t.Bottom < 0 || t.Left < 0 || t.Right < 0 {
		return fmt.Errorf("trim values must not be negative")
	}
	if t.Top+t.Bottom >= 1 || t.Left+t.Right >= 1 {
		return fmt.Errorf("opposite trim values must total less than 1")
	}

	return nil
}
