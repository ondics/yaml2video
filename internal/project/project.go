// Package project defines and loads video projects.
package project

import (
	"github.com/goccy/go-yaml"
	"github.com/ondics/yaml2video/internal/template"
	"github.com/ondics/yaml2video/internal/types"
)

// LoadOptions controls how a project is composed before it is parsed.
type LoadOptions struct {
	TemplatePaths []string
}

// Load reads a project from disk. A standard project may use scenes directly,
// or slides expanded by templates supplied through TemplatePaths.
func Load(path string, options LoadOptions) (*Project, error) {
	data, err := template.ExpandFile(path, template.Options{
		Paths: options.TemplatePaths,
	})
	if err != nil {
		return nil, err
	}

	return Parse(data)
}

// Parse decodes one complete project document. Use Load when the project is
// stored on disk or needs slides expanded from template files.
func Parse(data []byte) (*Project, error) {
	var value Project
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, err
	}

	return &value, nil
}

// Project is the complete definition of a video.
type Project struct {
	Version int `yaml:"version"`

	Video    Video    `yaml:"video"`
	Defaults Defaults `yaml:"defaults"`
	Music    Music    `yaml:"music"`
	Scenes   []Scene  `yaml:"scenes"`
}

type Video struct {
	Width      int         `yaml:"width"`
	Height     int         `yaml:"height"`
	FPS        int         `yaml:"fps"`
	Background types.Color `yaml:"background"`
}

type Defaults struct {
	Text     TextDefaults     `yaml:"text"`
	Subtitle SubtitleDefaults `yaml:"subtitle"`
}

type TextBackground struct {
	Color   types.Color `yaml:"color"`
	Opacity *float64    `yaml:"opacity"`
	Padding Padding     `yaml:"padding"`
	Radius  int         `yaml:"radius"`
}

type SubtitleDefaults struct {
	TextDefaults `yaml:",inline"`
	Transform    Transform `yaml:"transform"`
	MaxWidth     int       `yaml:"max_width"`
}

type TextDefaults struct {
	Font          string         `yaml:"font"`
	Size          int            `yaml:"size"`
	Color         types.Color    `yaml:"color"`
	Align         string         `yaml:"align"`
	VerticalAlign string         `yaml:"vertical_align"`
	Background    TextBackground `yaml:"background"`
}

type Music struct {
	Path    string  `yaml:"path"`
	Volume  float64 `yaml:"volume"`
	FadeOut float64 `yaml:"fade_out"`
}
