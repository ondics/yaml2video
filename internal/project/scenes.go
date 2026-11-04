package project

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/ondics/yaml2video/internal/types"
)

type Scene struct {
	ID         string           `yaml:"id"`
	Duration   *float64         `yaml:"duration"`
	Background *types.Color     `yaml:"background"`
	Layers     []Layer          `yaml:"layers"`
	Transition *SceneTransition `yaml:"transition"`
	Subtitles  []Subtitle       `yaml:"subtitles"`
}

type Subtitle struct {
	Duration   float64      `yaml:"duration"`
	Offset     *float64     `yaml:"offset"`
	Text       []TextItem   `yaml:"text"`
	Transition *Transitions `yaml:"transition"`
}

// Transitions describes the lifecycle transitions of an item. Scene Out is
// applied at the boundary to the following scene; subtitle transitions affect
// the subtitle while it is visible.
type Transitions struct {
	In  *Transition `yaml:"in"`
	Out *Transition `yaml:"out"`
}

// SceneTransition is retained as an alias for callers using the original API.
type SceneTransition = Transitions

type Transition struct {
	Type     string  `yaml:"type"`
	Duration float64 `yaml:"duration"`
}

type Layer struct {
	Type        LayerType  `yaml:"type"`
	Transform   *Transform `yaml:"transform"`
	Opacity     *float64   `yaml:"opacity"`
	LayerLayout `yaml:",inline"`

	LayerMedia `yaml:",inline"`
	LayerText  `yaml:",inline"`
	LayerShape `yaml:",inline"`
	LayerAudio `yaml:",inline"`
}

type LayerMedia struct {
	Path         string   `yaml:"path"`
	Fit          string   `yaml:"fit"`
	Duration     *float64 `yaml:"duration"`
	SourceOffset float64  `yaml:"source_offset"`
	Trim         *Trim    `yaml:"trim"`
}

type LayerText struct {
	Size       int             `yaml:"size"`
	Text       []TextItem      `yaml:"text"`
	Background *TextBackground `yaml:"background"`
}

type LayerShape struct {
	Radius float64      `yaml:"radius"`
	Color  *types.Color `yaml:"color"`
}

// LayerAudio uses Path from LayerImage and starts at the beginning of its scene.
// Its volume defaults to 1 when not set.
type LayerAudio struct {
	Volume *float64 `yaml:"volume"`
}

type TextItem struct {
	Long  *TextItemLong
	Short *TextItemShort
}

func (t *TextItem) UnmarshalYAML(b ast.Node) error {
	var short TextItemShort
	if err := yaml.NodeToValue(b, &short); err == nil {
		t.Short = &short
		t.Long = nil
		return nil
	}
	var long TextItemLong
	if err := yaml.NodeToValue(b, &long); err == nil {
		t.Long = &long
		t.Short = nil
		return nil
	}
	return fmt.Errorf("text item must be a string or an object")
}

type TextItemShort string

type TextItemLong struct {
	Content    string          `yaml:"content"`
	Bold       bool            `yaml:"bold"`
	Underline  bool            `yaml:"underline"`
	Italic     bool            `yaml:"italic"`
	Color      *types.Color    `yaml:"color"`
	Background *TextBackground `yaml:"background"`
}

type LayerType string

const (
	LayerTypeImage  LayerType = "image"
	LayerTypeVideo  LayerType = "video"
	LayerTypeText   LayerType = "text"
	LayerTypeAudio  LayerType = "audio"
	LayerTypeRect   LayerType = "rectangle"
	LayerTypeCircle LayerType = "circle"
)
