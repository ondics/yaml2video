// Package template expands shorthand slide documents into project YAML.
package template

import (
	"fmt"
	"os"

	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*\.?([[:alnum:]_-]+)\s*\}\}`)

// Options controls how a shorthand slide document is expanded.
type Options struct {
	Paths []string
}

type catalog struct {
	slides   map[string]slideTemplate
	video    any
	defaults any
	music    any
}

type slideTemplate struct {
	name     string
	required []string
	duration any
	scene    map[string]any
	source   string
}

// ExpandFile reads a YAML document and expands its slides using caller-provided
// template paths. Standard project documents are returned unchanged.
func ExpandFile(path string, options Options) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	document, err := decodeMap(data)
	if err != nil {
		return nil, fmt.Errorf("decode project document: %w", err)
	}
	if !hasKey(document, "slides") {
		return data, nil
	}

	templates, err := loadCatalog(options.Paths)
	if err != nil {
		return nil, err
	}
	applyCatalogDefaults(document, templates)
	if err := expandSlides(document, templates); err != nil {
		return nil, err
	}

	return yaml.Marshal(document)
}

func loadCatalog(paths []string) (catalog, error) {
	result := catalog{slides: make(map[string]slideTemplate)}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return catalog{}, fmt.Errorf("read template %q: %w", path, err)
		}

		raw, err := decodeMap(data)
		if err != nil {
			return catalog{}, fmt.Errorf("decode template %q: %w", path, err)
		}

		addCatalogDefaults(&result, raw)
		if err := addSlideTemplates(&result, raw, path); err != nil {
			return catalog{}, err
		}
	}

	return result, nil
}

func addCatalogDefaults(catalog *catalog, document map[string]any) {
	if catalog.video == nil && hasKey(document, "video") {
		catalog.video = cloneValue(document["video"])
	}
	if catalog.defaults == nil && hasKey(document, "defaults") {
		catalog.defaults = cloneValue(document["defaults"])
	}
	if catalog.music == nil && hasKey(document, "music") {
		catalog.music = cloneValue(document["music"])
	}
}

func addSlideTemplates(catalog *catalog, document map[string]any, source string) error {
	for _, definition := range slideTemplateDefinitions(document) {
		template, err := parseSlideTemplate(definition, source)
		if err != nil {
			return err
		}
		if _, exists := catalog.slides[template.name]; exists {
			return fmt.Errorf("duplicate slide template %q", template.name)
		}

		catalog.slides[template.name] = template
	}

	return nil
}

func slideTemplateDefinitions(document map[string]any) []map[string]any {
	definitions := make([]map[string]any, 0)

	for _, key := range []string{"slide_templates", "slide-templates", "slide-template"} {
		value, exists := document[key]
		if !exists {
			continue
		}
		for _, definition := range mapList(value) {
			definitions = append(definitions, definition)
		}
	}

	return definitions
}

func parseSlideTemplate(definition map[string]any, source string) (slideTemplate, error) {
	name := optionalString(definition, "name")
	if name == "" {
		return slideTemplate{}, fmt.Errorf("template %q: slide template name is required", source)
	}

	scene := mapWithout(definition, "name", "required", "duration", "scene")
	if explicitScene, exists := definition["scene"]; exists {
		var ok bool
		scene, ok = asMap(explicitScene)
		if !ok {
			return slideTemplate{}, fmt.Errorf("template %q: slide template %q scene must be a mapping", source, name)
		}
	}

	return slideTemplate{
		name:     name,
		required: stringList(definition["required"]),
		duration: definition["duration"],
		scene:    cloneMap(scene),
		source:   source,
	}, nil
}

func applyCatalogDefaults(document map[string]any, templates catalog) {
	if !hasKey(document, "video") && templates.video != nil {
		document["video"] = cloneValue(templates.video)
	}
	if !hasKey(document, "defaults") && templates.defaults != nil {
		document["defaults"] = cloneValue(templates.defaults)
	}
	if !hasKey(document, "music") && templates.music != nil {
		document["music"] = cloneValue(templates.music)
	}
}

func expandSlides(document map[string]any, templates catalog) error {
	slides := mapList(document["slides"])
	if len(slides) == 0 {
		return fmt.Errorf("slides must contain at least one slide")
	}

	scenes := make([]any, 0, len(slides))
	for index, slide := range slides {
		scene, err := expandSlide(slide, index, templates)
		if err != nil {
			return err
		}
		scenes = append(scenes, scene)
	}

	document["scenes"] = scenes
	delete(document, "slides")

	return nil
}

func expandSlide(slide map[string]any, index int, templates catalog) (map[string]any, error) {
	typeName := optionalString(slide, "type")
	if typeName == "" {
		return nil, fmt.Errorf("slide %d: type is required", index+1)
	}

	template, exists := templates.slides[typeName]
	if !exists {
		return nil, fmt.Errorf("slide %d: unknown template %q", index+1, typeName)
	}
	if err := validateRequiredValues(template, slide, index); err != nil {
		return nil, err
	}

	interpolated, err := interpolateMap(template.scene, slide)
	if err != nil {
		return nil, fmt.Errorf("slide %d (%s): %w", index+1, typeName, err)
	}

	interpolated["id"] = slideID(slide, typeName, index)
	duration, err := slideDuration(slide["duration"], template.duration)
	if err != nil {
		return nil, fmt.Errorf("slide %d (%s): %w", index+1, typeName, err)
	}
	interpolated["duration"] = duration

	return interpolated, nil
}

func validateRequiredValues(template slideTemplate, slide map[string]any, index int) error {
	for _, name := range template.required {
		if isBlank(slide[name]) {
			return fmt.Errorf("slide %d (%s): required value %q is missing", index+1, template.name, name)
		}
	}

	return nil
}

func slideID(slide map[string]any, typeName string, index int) string {
	if id := optionalString(slide, "id"); id != "" {
		return id
	}

	return fmt.Sprintf("%s-%03d", typeName, index+1)
}

func slideDuration(value, fallback any) (float64, error) {
	if value == nil {
		value = fallback
	}
	if value == nil {
		return 0, fmt.Errorf("duration is required")
	}

	duration, err := parseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %w", err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}

	return duration, nil
}

func parseDuration(value any) (float64, error) {
	switch value := value.(type) {
	case int:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case float64:
		return value, nil
	case string:
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			return seconds, nil
		}

		duration, err := time.ParseDuration(value)
		if err != nil {
			return 0, fmt.Errorf("use seconds (for example 5) or a Go duration (for example 5s)")
		}
		return duration.Seconds(), nil
	default:
		return 0, fmt.Errorf("must be a number of seconds or a duration string")
	}
}

func interpolateMap(value map[string]any, variables map[string]any) (map[string]any, error) {
	interpolated, err := interpolateValue(value, variables)
	if err != nil {
		return nil, err
	}

	return interpolated.(map[string]any), nil
}

func interpolateValue(value any, variables map[string]any) (any, error) {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, nested := range value {
			interpolated, err := interpolateValue(nested, variables)
			if err != nil {
				return nil, err
			}
			result[key] = interpolated
		}
		return result, nil
	case []any:
		result := make([]any, len(value))
		for index, nested := range value {
			interpolated, err := interpolateValue(nested, variables)
			if err != nil {
				return nil, err
			}
			result[index] = interpolated
		}
		return result, nil
	case string:
		return interpolateString(value, variables)
	default:
		return value, nil
	}
}

func interpolateString(value string, variables map[string]any) (string, error) {
	var interpolationError error
	result := placeholderPattern.ReplaceAllStringFunc(value, func(placeholder string) string {
		matches := placeholderPattern.FindStringSubmatch(placeholder)
		name := matches[1]
		replacement, exists := variables[name]
		if !exists || isBlank(replacement) {
			interpolationError = fmt.Errorf("value %q is required by template", name)
			return placeholder
		}

		return fmt.Sprint(replacement)
	})
	if interpolationError != nil {
		return "", interpolationError
	}

	return result, nil
}

func decodeMap(data []byte) (map[string]any, error) {
	var value map[string]any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("document must be a mapping")
	}

	return value, nil
}

func asMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func mapList(value any) []map[string]any {
	if mapping, ok := asMap(value); ok {
		return []map[string]any{mapping}
	}

	values, ok := value.([]any)
	if !ok {
		return nil
	}

	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if mapping, ok := asMap(value); ok {
			result = append(result, mapping)
		}
	}

	return result
}

func stringList(value any) []string {
	switch value := value.(type) {
	case string:
		return splitCommaSeparated(value)
	case []any:
		result := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
		return result
	default:
		return nil
	}
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}

	return result
}

func optionalString(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}

func hasKey(value map[string]any, key string) bool {
	_, exists := value[key]
	return exists
}

func isBlank(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}

	return false
}

func mapWithout(value map[string]any, keys ...string) map[string]any {
	result := cloneMap(value)
	for _, key := range keys {
		delete(result, key)
	}

	return result
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, nested := range value {
		result[key] = cloneValue(nested)
	}

	return result
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneMap(value)
	case []any:
		result := make([]any, len(value))
		for index, nested := range value {
			result[index] = cloneValue(nested)
		}
		return result
	default:
		return value
	}
}
