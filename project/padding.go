package project

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

// Padding is symmetric when decoded from one value and axis-specific when
// decoded from a two-item [x, y] YAML sequence.
type Padding struct {
	X int
	Y int
}

// UnmarshalYAML accepts either `padding: 8` or `padding: [8, 4]`.
func (p *Padding) UnmarshalYAML(node ast.Node) error {
	var uniform int
	if err := yaml.NodeToValue(node, &uniform); err == nil {
		if uniform < 0 {
			return fmt.Errorf("padding must not be negative")
		}

		p.X = uniform
		p.Y = uniform
		return nil
	}

	var values []int
	if err := yaml.NodeToValue(node, &values); err != nil {
		return fmt.Errorf("padding must be a non-negative integer or [x, y] pair")
	}

	if len(values) != 2 {
		return fmt.Errorf("padding pair must contain exactly two values")
	}
	if values[0] < 0 || values[1] < 0 {
		return fmt.Errorf("padding must not be negative")
	}

	p.X = values[0]
	p.Y = values[1]
	return nil
}
