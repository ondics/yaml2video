package project

import (
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

type Transform struct {
	Custom *CustomTransform
	Preset *TransformPreset
}

func (t *Transform) UnmarshalYAML(b ast.Node) error {
	switch b.(type) {
	case *ast.StringNode:
		var preset TransformPreset
		if err := yaml.NodeToValue(b, &preset); err != nil {
			return err
		}
		t.Preset = &preset
		t.Custom = nil
	default:
		custom := CustomTransform{ScaleX: 1, ScaleY: 1}
		if err := yaml.NodeToValue(b, &custom); err != nil {
			return err
		}
		t.Custom = &custom
		t.Preset = nil
	}
	return nil
}

type TransformPreset string

const (
	TransformPresetCenter       TransformPreset = "center"
	TransformPresetCenterLeft   TransformPreset = "center-left"
	TransformPresetCenterRight  TransformPreset = "center-right"
	TransformPresetTopCenter    TransformPreset = "top-center"
	TransformPresetTopLeft      TransformPreset = "top-left"
	TransformPresetTopRight     TransformPreset = "top-right"
	TransformPresetBottomCenter TransformPreset = "bottom-center"
	TransformPresetBottomLeft   TransformPreset = "bottom-left"
	TransformPresetBottomRight  TransformPreset = "bottom-right"
)

func (t TransformPreset) Valid() bool {
	switch t {
	case TransformPresetCenter, TransformPresetCenterLeft, TransformPresetCenterRight, TransformPresetTopCenter, TransformPresetTopLeft, TransformPresetTopRight, TransformPresetBottomCenter, TransformPresetBottomLeft, TransformPresetBottomRight:

		return true
	default:
		return false
	}
}

type CustomTransform struct {
	X      float64 `yaml:"x"`
	Y      float64 `yaml:"y"`
	ScaleX float64 `yaml:"scale_x"`
	ScaleY float64 `yaml:"scale_y"`
	Rotate float64 `yaml:"rotate"`
}
