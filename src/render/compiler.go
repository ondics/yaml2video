package render

import (
	"fmt"
	"math"
	"path/filepath"
	"time"

	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/ondics/yaml2video/src/project"
	"github.com/ondics/yaml2video/src/types"
)

const (
	defaultWorkDir = ".out"
	defaultOutput  = "output.mp4"
	defaultFit     = "cover"
)

// Options controls where compilation writes its intermediate and final files.
type Options struct {
	WorkDir string
	Output  string
}

type pixelBounds struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Compile validates a project and converts it into an FFmpeg render plan.
func Compile(p *project.Project, options Options) (*Plan, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}

	plan := newPlan(p, options)
	start := time.Duration(0)

	for index, scene := range p.Scenes {
		compiled, err := compileScene(p, scene, index, plan.WorkDir, start)
		if err != nil {
			return nil, fmt.Errorf("scene %q: %w", scene.ID, err)
		}

		plan.Scenes = append(plan.Scenes, compiled)
		start += compiled.Duration
	}

	addBoundaryTransitions(plan, p.Scenes)
	plan.Duration += start
	updateSceneStarts(plan)

	return plan, nil
}

func newPlan(p *project.Project, options Options) *Plan {
	background, _ := p.Video.Background.FFmpeg()

	workDir := options.WorkDir
	if workDir == "" {
		workDir = defaultWorkDir
	}

	output := options.Output
	if output == "" {
		output = defaultOutput
	}

	plan := &Plan{
		Video: VideoSpec{
			Width:      p.Video.Width,
			Height:     p.Video.Height,
			FPS:        p.Video.FPS,
			Background: background,
		},
		WorkDir: workDir,
		Output:  output,
	}

	if p.Music.Path != "" {
		plan.Music = &MusicPlan{
			Path:    p.Music.Path,
			Volume:  p.Music.Volume,
			FadeOut: seconds(p.Music.FadeOut),
		}
	}

	return plan
}

func compileScene(p *project.Project, scene project.Scene, index int, workDir string, start time.Duration) (ScenePlan, error) {
	duration := seconds(*scene.Duration)
	background := sceneBackground(p.Video.Background, scene.Background)
	assPath, outputPath := sceneArtifactPaths(workDir, index, scene.ID)

	compiled := ScenePlan{
		ID:         scene.ID,
		Start:      start,
		Duration:   duration,
		Background: background,
		ASSPath:    assPath,
		Output:     outputPath,
	}

	for _, layer := range scene.Layers {
		layerPlan, err := compileLayer(p, layer)
		if err != nil {
			return ScenePlan{}, err
		}

		if layer.Type == project.LayerTypeAudio {
			compiled.Audio = append(compiled.Audio, compileAudioLayer(layer, layerPlan))
			continue
		}

		compiled.Layers = append(compiled.Layers, layerPlan)
	}

	subtitles, err := compileSubtitles(p, scene.Subtitles)
	if err != nil {
		return ScenePlan{}, err
	}

	compiled.Subtitles = subtitles
	compiled.ASS = assDocument(p.Video.Width, p.Video.Height, compiled)

	return compiled, nil
}

func sceneBackground(defaultColor types.Color, override *types.Color) string {
	color := defaultColor
	if override != nil {
		color = *override
	}

	background, _ := color.FFmpeg()
	return background
}

func sceneArtifactPaths(workDir string, index int, sceneID string) (assPath, outputPath string) {
	filename := fmt.Sprintf("%03d-%s", index, safeName(sceneID))

	return filepath.Join(workDir, "ass", filename+".ass"),
		filepath.Join(workDir, "scenes", filename+".mkv")
}

func compileAudioLayer(layer project.Layer, plan LayerPlan) AudioLayerPlan {
	volume := 1.0
	if layer.Volume != nil {
		volume = *layer.Volume
	}

	return AudioLayerPlan{
		Path:         plan.Path,
		Volume:       volume,
		SourceOffset: plan.SourceOffset,
		Duration:     plan.Duration,
	}
}

func compileSubtitles(p *project.Project, subtitles []project.Subtitle) ([]SubtitlePlan, error) {
	background, err := compileTextBackground(p.Defaults.Subtitle.Background)
	if err != nil {
		return nil, err
	}

	plans := make([]SubtitlePlan, 0, len(subtitles))
	offset := time.Duration(0)

	for _, subtitle := range subtitles {
		if subtitle.Offset != nil {
			offset = seconds(*subtitle.Offset)
		}

		spans, err := compileTextSpans(subtitle.Text, p.Defaults.Subtitle.Color)
		if err != nil {
			return nil, err
		}

		end := offset + seconds(subtitle.Duration)
		plans = append(plans, SubtitlePlan{
			Spans:      spans,
			Font:       p.Defaults.Subtitle.Font,
			FontSize:   p.Defaults.Subtitle.Size,
			Color:      colorString(p.Defaults.Subtitle.Color),
			X:          p.Video.Width / 2,
			Y:          p.Video.Height - 16,
			Start:      offset,
			End:        end,
			In:         compileTransition(subtitle.Transition, true),
			Out:        compileTransition(subtitle.Transition, false),
			Background: background,
		})

		offset = end
	}

	return plans, nil
}

func addBoundaryTransitions(plan *Plan, scenes []project.Scene) {
	for index := 0; index < len(scenes)-1; index++ {
		transition := compileTransition(scenes[index].Transition, false)
		if transition == nil {
			continue
		}

		plan.Transitions = append(plan.Transitions, BoundaryTransition{
			FromScene: index,
			Type:      transition.Type,
			Duration:  transition.Duration,
			Offset:    plan.Scenes[index].Start + plan.Scenes[index].Duration - transition.Duration,
		})
		plan.Duration -= transition.Duration
	}
}

// Validate verifies that the project can be compiled into a render plan.
func Validate(p *project.Project) error {
	if p == nil {
		return fmt.Errorf("project is required")
	}
	if p.Video.Width <= 0 || p.Video.Height <= 0 || p.Video.FPS <= 0 {
		return fmt.Errorf("video width, height, and fps must be positive")
	}
	if _, err := p.Video.Background.FFmpeg(); err != nil {
		return fmt.Errorf("video background: %w", err)
	}
	if len(p.Scenes) == 0 {
		return fmt.Errorf("at least one scene is required")
	}

	seenIDs := make(map[string]bool, len(p.Scenes))
	for index, scene := range p.Scenes {
		if err := validateScene(scene, index, seenIDs); err != nil {
			return err
		}
	}

	return nil
}

func validateScene(scene project.Scene, index int, seenIDs map[string]bool) error {
	if scene.ID == "" {
		return fmt.Errorf("scene %d: id is required", index)
	}
	if seenIDs[scene.ID] {
		return fmt.Errorf("scene %q: id must be unique", scene.ID)
	}
	seenIDs[scene.ID] = true

	if scene.Duration == nil || *scene.Duration <= 0 {
		return fmt.Errorf("scene %q: positive duration is required", scene.ID)
	}
	if scene.Background != nil {
		if _, err := scene.Background.FFmpeg(); err != nil {
			return fmt.Errorf("scene %q background: %w", scene.ID, err)
		}
	}
	if err := validateTransitions(scene.Transition, *scene.Duration, "scene "+scene.ID); err != nil {
		return err
	}
	if scene.Transition != nil && scene.Transition.In != nil {
		return fmt.Errorf("scene %q: only transition.out is valid; it defines the boundary to the following scene", scene.ID)
	}

	for _, layer := range scene.Layers {
		if err := validateMediaLayer(scene.ID, *scene.Duration, layer); err != nil {
			return err
		}
	}

	return validateSubtitles(scene.ID, *scene.Duration, scene.Subtitles)
}

func validateMediaLayer(sceneID string, sceneDuration float64, layer project.Layer) error {
	if layer.Type != project.LayerTypeVideo && layer.Type != project.LayerTypeAudio {
		return nil
	}
	if layer.SourceOffset < 0 {
		return fmt.Errorf("scene %q: %s source_offset must not be negative", sceneID, layer.Type)
	}
	if layer.Duration != nil && (*layer.Duration <= 0 || *layer.Duration > sceneDuration) {
		return fmt.Errorf("scene %q: %s duration must be positive and no longer than the scene", sceneID, layer.Type)
	}

	return nil
}

func validateSubtitles(sceneID string, sceneDuration float64, subtitles []project.Subtitle) error {
	offset := 0.0

	for _, subtitle := range subtitles {
		if subtitle.Duration <= 0 {
			return fmt.Errorf("scene %q: subtitle duration must be positive", sceneID)
		}
		if subtitle.Offset != nil {
			offset = *subtitle.Offset
		}
		if offset+subtitle.Duration > sceneDuration {
			return fmt.Errorf("scene %q: subtitle extends beyond the scene duration", sceneID)
		}

		offset += subtitle.Duration
		if err := validateTransitions(subtitle.Transition, subtitle.Duration, "subtitle"); err != nil {
			return err
		}
	}

	return nil
}

func validateTransitions(values *project.SceneTransition, maximum float64, owner string) error {
	if values == nil {
		return nil
	}

	for _, transition := range []*project.Transition{values.In, values.Out} {
		if transition == nil {
			continue
		}
		if transition.Duration <= 0 || transition.Duration > maximum {
			return fmt.Errorf("%s transition duration must be within its visible duration", owner)
		}
		if transition.Type != "fade" && transition.Type != "dissolve" {
			return fmt.Errorf("%s transition type %q is unsupported", owner, transition.Type)
		}
		if owner == "subtitle" && transition.Type != "fade" {
			return fmt.Errorf("subtitle transition type %q is unsupported; subtitles currently support fade", transition.Type)
		}
	}

	return nil
}

func compileLayer(p *project.Project, layer project.Layer) (LayerPlan, error) {
	opacity, err := layerOpacity(layer)
	if err != nil {
		return LayerPlan{}, err
	}

	plan := LayerPlan{
		Kind:    string(layer.Type),
		Opacity: opacity,
	}
	if layer.Type == project.LayerTypeText {
		return compileTextLayer(p, layer, plan)
	}

	bounds, err := resolveTransform(p.Video.Width, p.Video.Height, layer.LayerLayout, layer.Transform)
	if err != nil {
		return LayerPlan{}, err
	}
	plan.X = bounds.X
	plan.Y = bounds.Y
	plan.Width = bounds.Width
	plan.Height = bounds.Height

	switch layer.Type {
	case project.LayerTypeImage, project.LayerTypeVideo:
		return compileMediaLayer(layer, plan)
	case project.LayerTypeRect, project.LayerTypeCircle:
		return compileShapeLayer(layer, plan)
	case project.LayerTypeAudio:
		return compileAudioSource(layer, plan)
	default:
		return plan, fmt.Errorf("unsupported layer type %q", layer.Type)
	}
}

func layerOpacity(layer project.Layer) (float64, error) {
	opacity := 1.0
	if layer.Opacity != nil {
		opacity = *layer.Opacity
	}
	if opacity < 0 || opacity > 1 {
		return 0, fmt.Errorf("layer opacity must be between 0 and 1")
	}

	return opacity, nil
}

func compileMediaLayer(layer project.Layer, plan LayerPlan) (LayerPlan, error) {
	if layer.Path == "" {
		return plan, fmt.Errorf("%s path is required", layer.Type)
	}

	fit := layer.Fit
	if fit == "" {
		fit = defaultFit
	}
	if fit != "cover" && fit != "contain" {
		return plan, fmt.Errorf("unsupported image fit %q", fit)
	}

	plan.Path = layer.Path
	plan.Fit = fit

	if layer.Type == project.LayerTypeVideo {
		plan.SourceOffset = seconds(layer.SourceOffset)
		if layer.Duration != nil {
			plan.Duration = seconds(*layer.Duration)
		}
	}
	if layer.Trim != nil {
		if err := layer.Trim.Validate(); err != nil {
			return plan, fmt.Errorf("%s trim: %w", layer.Type, err)
		}
		plan.Trim = &TrimPlan{
			Top:    layer.Trim.Top,
			Bottom: layer.Trim.Bottom,
			Left:   layer.Trim.Left,
			Right:  layer.Trim.Right,
		}
	}

	return plan, nil
}

func compileShapeLayer(layer project.Layer, plan LayerPlan) (LayerPlan, error) {
	if layer.Color == nil {
		return plan, fmt.Errorf("%s color is required", layer.Type)
	}

	color, err := layer.Color.FFmpeg()
	if err != nil {
		return plan, err
	}

	plan.Color = color
	return plan, nil
}

func compileAudioSource(layer project.Layer, plan LayerPlan) (LayerPlan, error) {
	if layer.Path == "" {
		return plan, fmt.Errorf("audio path is required")
	}
	if layer.Volume != nil && *layer.Volume < 0 {
		return plan, fmt.Errorf("audio volume must not be negative")
	}

	plan.Path = layer.Path
	plan.SourceOffset = seconds(layer.SourceOffset)
	if layer.Duration != nil {
		plan.Duration = seconds(*layer.Duration)
	}

	return plan, nil
}

func compileTextLayer(p *project.Project, layer project.Layer, plan LayerPlan) (LayerPlan, error) {
	plan.face = defaultFonts.face(p.Defaults.Text.Font, 400, 0)
	plan.Font = plan.face.Name
	plan.FontSize = p.Defaults.Text.Size
	if layer.Size > 0 {
		plan.FontSize = layer.Size
	}
	plan.Color = colorString(p.Defaults.Text.Color)

	background := p.Defaults.Text.Background
	if layer.Background != nil {
		background = *layer.Background
	}

	compiledBackground, err := compileTextBackground(background)
	if err != nil {
		return plan, err
	}
	plan.Background = compiledBackground

	spans, err := compileTextSpans(layer.Text, p.Defaults.Text.Color)
	if err != nil {
		return plan, err
	}
	if len(spans) == 0 {
		return plan, fmt.Errorf("text content is required")
	}

	metrics := measureText(spans, plan.Font, plan.FontSize)
	layout := textLayout(layer.LayerLayout, metrics)
	bounds, err := resolveTransform(p.Video.Width, p.Video.Height, layout, layer.Transform)
	if err != nil {
		return plan, err
	}

	plan.X = bounds.X
	plan.Y = bounds.Y
	plan.Width = bounds.Width
	plan.Height = bounds.Height

	if layer.Width != nil {
		plan.WrapWidth = plan.Width
		spans = wrapTextSpans(spans, plan.WrapWidth, plan.Font, plan.FontSize)
		metrics = measureText(spans, plan.Font, plan.FontSize)

		if layer.Height == nil {
			layout = textLayout(layer.LayerLayout, metrics)
			bounds, err = resolveTransform(p.Video.Width, p.Video.Height, layout, layer.Transform)
			if err != nil {
				return plan, err
			}
			plan.X = bounds.X
			plan.Y = bounds.Y
			plan.Width = bounds.Width
			plan.Height = bounds.Height
		}
	}

	plan.Spans = spans
	plan.ItemBackgrounds = textItemBackgrounds(spans, metrics, plan)
	return plan, nil
}

// resolveTransform converts a transform from canvas-relative values to pixel bounds.
func resolveTransform(
	canvasWidth, canvasHeight int,
	layout project.LayerLayout,
	transform *project.Transform,
) (pixelBounds, error) {
	bounds, err := layerBounds(canvasWidth, canvasHeight, layout)
	if err != nil {
		return pixelBounds{}, err
	}
	if transform == nil {
		return bounds, nil
	}

	if transform.Custom != nil {
		return resolveCustomTransform(canvasWidth, canvasHeight, layout, bounds, transform.Custom)
	}
	if transform.Preset == nil || !transform.Preset.Valid() {
		return pixelBounds{}, fmt.Errorf("invalid transform")
	}

	bounds.X = presetX(*transform.Preset, canvasWidth, bounds.Width)
	bounds.Y = presetY(*transform.Preset, canvasHeight, bounds.Height)
	return bounds, nil
}

func layerBounds(canvasWidth, canvasHeight int, layout project.LayerLayout) (pixelBounds, error) {
	bounds := pixelBounds{Width: canvasWidth, Height: canvasHeight}
	if layout.Width != nil {
		if *layout.Width <= 0 {
			return pixelBounds{}, fmt.Errorf("layer width must be positive")
		}
		bounds.Width = *layout.Width
	}
	if layout.Height != nil {
		if *layout.Height <= 0 {
			return pixelBounds{}, fmt.Errorf("layer height must be positive")
		}
		bounds.Height = *layout.Height
	}

	return bounds, nil
}

func resolveCustomTransform(
	canvasWidth, canvasHeight int,
	layout project.LayerLayout,
	bounds pixelBounds,
	transform *project.CustomTransform,
) (pixelBounds, error) {
	if transform.X < 0 || transform.X > 1 || transform.Y < 0 || transform.Y > 1 || transform.ScaleX <= 0 || transform.ScaleY <= 0 {
		return pixelBounds{}, fmt.Errorf("custom transform x/y must be in [0,1] and scales must be positive")
	}
	if transform.ScaleX != 1 && layout.Width == nil {
		return pixelBounds{}, fmt.Errorf("transform scale_x requires an explicit layer width")
	}
	if transform.ScaleY != 1 && layout.Height == nil {
		return pixelBounds{}, fmt.Errorf("transform scale_y requires an explicit layer height")
	}

	bounds.Width = int(math.Round(float64(bounds.Width) * transform.ScaleX))
	bounds.Height = int(math.Round(float64(bounds.Height) * transform.ScaleY))
	bounds.X = int(math.Round(float64(canvasWidth)*transform.X)) - bounds.Width/2
	bounds.Y = int(math.Round(float64(canvasHeight)*transform.Y)) - bounds.Height/2

	return bounds, nil
}

func presetX(preset project.TransformPreset, canvasWidth, width int) int {
	switch preset {
	case project.TransformPresetCenterLeft, project.TransformPresetTopLeft, project.TransformPresetBottomLeft:
		return 0
	case project.TransformPresetCenterRight, project.TransformPresetTopRight, project.TransformPresetBottomRight:
		return canvasWidth - width
	default:
		return (canvasWidth - width) / 2
	}
}

func presetY(preset project.TransformPreset, canvasHeight, height int) int {
	switch preset {
	case project.TransformPresetTopLeft, project.TransformPresetTopCenter, project.TransformPresetTopRight:
		return 0
	case project.TransformPresetBottomLeft, project.TransformPresetBottomCenter, project.TransformPresetBottomRight:
		return canvasHeight - height
	default:
		return (canvasHeight - height) / 2
	}
}

func updateSceneStarts(plan *Plan) {
	start := time.Duration(0)

	for index := range plan.Scenes {
		scene := &plan.Scenes[index]
		scene.Start = start
		start += scene.Duration

		if transition := boundaryFor(plan.Transitions, index); transition != nil {
			transition.Offset = start - transition.Duration
			start -= transition.Duration
		}
	}
}

func compileTransition(transitions *project.SceneTransition, in bool) *TransitionPlan {
	if transitions == nil {
		return nil
	}

	transition := transitions.Out
	if in {
		transition = transitions.In
	}
	if transition == nil {
		return nil
	}

	return &TransitionPlan{
		Type:     transition.Type,
		Duration: seconds(transition.Duration),
	}
}

func seconds(value float64) time.Duration {
	return time.Duration(math.Round(value * float64(time.Second)))
}

func compileTextSpans(items []project.TextItem, defaultColor types.Color) ([]TextSpan, error) {
	spans := make([]TextSpan, 0, len(items))
	for _, item := range items {
		if item.Short != nil {
			spans = append(spans, TextSpan{
				Content: string(*item.Short),
				Color:   colorString(defaultColor),
			})
		}
		if item.Long != nil {
			color := defaultColor
			if item.Long.Color != nil {
				color = *item.Long.Color
			}

			var itemBackground *TextBackgroundPlan
			if item.Long.Background != nil {
				compiledBackground, err := compileTextBackground(*item.Long.Background)
				if err != nil {
					return nil, err
				}
				itemBackground = compiledBackground
			}

			spans = append(spans, TextSpan{
				Content:    item.Long.Content,
				Color:      colorString(color),
				Bold:       item.Long.Bold,
				Italic:     item.Long.Italic,
				Underline:  item.Long.Underline,
				Background: itemBackground,
			})
		}
	}

	return spans, nil
}

func compileTextBackground(background project.TextBackground) (*TextBackgroundPlan, error) {
	if background.Color == "" {
		return nil, nil
	}
	opacity := 1.0
	if background.Opacity != nil {
		opacity = *background.Opacity
	}
	if opacity < 0 || opacity > 1 {
		return nil, fmt.Errorf("text background opacity must be between 0 and 1")
	}
	if background.Padding.X < 0 || background.Padding.Y < 0 {
		return nil, fmt.Errorf("text background padding must not be negative")
	}
	if background.Radius < 0 {
		return nil, fmt.Errorf("text background radius must not be negative")
	}

	color, err := background.Color.FFmpeg()
	if err != nil {
		return nil, fmt.Errorf("text background color: %w", err)
	}

	return &TextBackgroundPlan{
		Color:    color,
		Opacity:  opacity,
		PaddingX: background.Padding.X,
		PaddingY: background.Padding.Y,
		Radius:   background.Radius,
	}, nil
}

// Package v2 loads and composes version 2 video and template documents.

type obj = map[string]any

func object(v any) obj {
	x, _ := v.(map[string]any)
	if x == nil {
		return obj{}
	}
	return x
}

func str(v any) string  { s, _ := v.(string); return s }
func num(v any) float64 { n, _ := number(v); return n }
func integer(v any) int { return int(num(v)) }
func flag(v any) bool   { return v == true }

func duration(v any) (time.Duration, error) {
	if v == nil {
		return 0, nil
	}
	var seconds float64
	if s, ok := v.(string); ok {
		seconds, _ = strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
	} else {
		seconds = num(v)
	}
	if seconds <= 0 || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0, fmt.Errorf("invalid duration %v", v)
	}
	return time.Duration(math.Round(seconds * float64(time.Second))), nil
}

func offset(v any) (time.Duration, error) {
	if v == nil || v == json.Number("0") || v == "0s" {
		return 0, nil
	}
	return duration(v)
}

func choose(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func path(base string, v any) (string, error) {
	s := str(v)
	if s == "" {
		return "", nil
	}
	if strings.Contains(s, "://") {
		if _, err := mediaURL(s); err != nil {
			return "", err
		}
		return s, nil
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(base, s)
	}

	s = filepath.Clean(s)
	f, err := os.Open(s)
	if err != nil {
		return "", fmt.Errorf("media %s: %w", s, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() {
		return "", fmt.Errorf("media %s is not a regular file", s)
	}

	return s, nil
}

// Load validates the documents and builds a render plan. An empty templatePath
// uses video.template_file, relative to the video YAML's directory.
// Media paths are relative to videoPath. Work artifacts are placed in .yaml2video.
func Load(videoPath, templatePath string) (*Plan, error) {
	return LoadWithTemplateBase(videoPath, templatePath, "")
}

// LoadWithTemplateBase is like Load, but resolves a relative video.template_file
// from templateBaseDir when provided. Explicit templatePath and media paths are
// unaffected by this override.
func LoadWithTemplateBase(videoPath, templatePath, templateBaseDir string) (*Plan, error) {
	v, e := document(videoPath, "video.schema.json")
	if e != nil {
		return nil, e
	}

	if templatePath == "" {
		templatePath = str(v["template_file"])
		if templatePath == "" {
			return nil, fmt.Errorf("a template YAML file is required: set template_file in the video YAML or provide -t template.yaml")
		}
		if !filepath.IsAbs(templatePath) {
			base := templateBaseDir
			if base == "" {
				base = filepath.Dir(videoPath)
			}
			templatePath = filepath.Join(base, templatePath)
		}
	}

	t, e := document(templatePath, "template.schema.json")
	if e != nil {
		return nil, e
	}
	return loadDocuments(v, t, filepath.Dir(videoPath))
}

// FromBytes validates video and template YAML or JSON bytes and builds a render plan.
// Media paths are relative to baseDir. Work artifacts are placed in .yaml2video.
// Documents are supplied in memory, schemas are embedded, and media may be local or HTTP(S).
func FromBytes(videoData, templateData []byte, baseDir string) (*Plan, error) {
	v, err := documentBytes(videoData, "video", "video.schema.json")
	if err != nil {
		return nil, err
	}

	t, err := documentBytes(templateData, "template", "template.schema.json")
	if err != nil {
		return nil, err
	}

	return loadDocuments(v, t, baseDir)
}

func loadDocuments(v, t obj, base string) (*Plan, error) {
	if str(v["template"]) != str(t["template_id"]) {
		return nil, fmt.Errorf("video template %q does not match template_id %q", v["template"], t["template_id"])
	}

	return compose(v, t, base)
}

func compose(v, t obj, base string) (*Plan, error) {
	format := object(t["format"])
	defaults := object(t["defaults"])
	theme := object(defaults["theme"])
	timing := object(defaults["timing"])
	audioDefaults := object(defaults["audio"])
	layout := object(t["layout"])
	content := object(v["content"])
	assets := object(v["assets"])
	p := &Plan{
		Video: VideoSpec{Width: integer(format["width"]),
			Height:     integer(format["height"]),
			FPS:        integer(format["fps"]),
			CRF:        integer(format["crf"]),
			Background: backgroundColor(theme["background"])},
		WorkDir: filepath.Join(base, ".yaml2video"),
		Output:  filepath.Join(base, "output.mp4"),
	}
	// Font files in fonts/ next to the video, and any listed in the template.
	fontDirs := []string{filepath.Join(base, "fonts")}
	for _, entry := range list(object(defaults["typography"])["fonts"]) {
		if strings.Contains(str(entry), "://") {
			return nil, fmt.Errorf("defaults.typography.fonts: font files must be local: %q", str(entry))
		}
		dir, err := path(base, entry)
		if err != nil {
			return nil, fmt.Errorf("defaults.typography.fonts: %w", err)
		}
		fontDirs = append(fontDirs, dir)
	}
	p.Video.fonts = fontBookFor(fontDirs)

	logo, err := mediaPath(base, assets["logo"], "image")
	if err != nil {
		return nil, err
	}

	sections := object(t["composition"])["sections"].([]any)
	if err := validateLayoutNames(layout); err != nil {
		return nil, err
	}
	adv := newAdvancedContext(p, v, t, base)
	meta := object(v["metadata"])

	// Each scene's own out and in transitions; lists in a section rotate per scene.
	type sceneTransitions struct {
		out, in obj
	}
	var sceneTr []sceneTransitions
	ids := map[string]bool{}
	sceneIDs := map[string]bool{}
	hasSlides := false
	for _, raw := range sections {
		section := object(raw)
		id := str(section["id"])
		if ids[id] {
			return nil, fmt.Errorf("duplicate section id %s", id)
		}

		ids[id] = true
		role := str(section["role"])

		var items []any
		switch role {
		case "intro", "outro":
			if item := content[role]; item != nil {
				items = []any{item}
			}
		case "slide":
			hasSlides = true
			items = content["slides"].([]any)
		}
		if role != "slide" && flag(section["repeat"]) {
			return nil, fmt.Errorf("%s: repeat is only supported for slides", id)
		}
		for i, rawItem := range items {
			item := object(rawItem)
			name := id
			if role == "slide" {
				name = fmt.Sprintf("%s-%d", id, i+1)
				if s := str(item["id"]); s != "" {
					name = id + "-" + s
				}
			}

			if sceneIDs[name] {
				return nil, fmt.Errorf("duplicate scene id %q", name)
			}
			sceneIDs[name] = true

			layoutName, layoutDef, err := resolveLayout(layout, role, section, item)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			// A custom layout can carry scene defaults: duration and transitions.
			sceneDefaults := object(layoutDef)

			d, err := duration(choose(item["duration"], sceneDefaults["duration"], section["default_duration"], timing[role+"_duration"]))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			scene := ScenePlan{ID: name, Duration: d, Role: role, Layout: layoutName, item: item, index: i, total: len(items)}
			// Bindings carry paths as written; elements resolve them like content paths.
			scene.bindings = sceneBindings(item, name, role, layoutName, i, len(items), str(assets["logo"]), meta)
			sceneIndex := len(p.Scenes)
			if isCustomLayout(layoutDef) {
				elements, background := customLayout(layoutDef, theme)
				if err := composeBackground(&scene, background, p, base, object(defaults["media"])); err != nil {
					return nil, fmt.Errorf("%s.background: %w", name, err)
				}
				if err := adv.compileCustomLayout(&scene, sceneIndex, elements); err != nil {
					return nil, fmt.Errorf("%s: layout %q: %w", name, layoutName, err)
				}
			} else if err := composeClassicScene(&scene, object(layoutDef), item, p, base, defaults, assets, logo); err != nil {
				return nil, err
			}

			track := object(item["audio"])
			if len(track) > 0 {
				a, err := audioLayer(track, audioDefaults, base, 0, d)
				if err != nil {
					return nil, fmt.Errorf("%s.audio: %w", name, err)
				}
				scene.Audio = append(scene.Audio, a)
			}

			if effects, ok := item["sound_effects"].([]any); ok {
				for j, raw := range effects {
					fx := object(raw)
					at, err := offset(fx["at"])
					if err != nil || at >= d {
						return nil, fmt.Errorf("%s.sound_effects[%d]: offset outside scene", name, j)
					}

					a, err := audioLayer(fx, audioDefaults, base, at, d-at)
					if err != nil {
						return nil, fmt.Errorf("%s.sound_effects[%d]: %w", name, j, err)
					}
					a.SoundEffect = true

					scene.Audio = append(scene.Audio, a)
				}
			}

			scene.Output = filepath.Join(p.WorkDir, "scenes", fmt.Sprintf("scene-%04d.mkv", sceneIndex))
			p.Scenes = append(p.Scenes, scene)
			// The item's transitions win over its layout's, then its section's.
			sceneTr = append(sceneTr, sceneTransitions{
				out: pick(choose(item["transition_out"], sceneDefaults["transition_out"], section["transition_out"], timing["transition"]), i),
				in:  pick(choose(item["transition_in"], sceneDefaults["transition_in"], section["transition_in"]), i),
			})
		}
	}
	if n := len(p.Scenes); n > 0 {
		if p.Scenes[n-1].item["transition_out"] != nil {
			return nil, fmt.Errorf("%s: the last scene has no transition_out", p.Scenes[n-1].ID)
		}
		if p.Scenes[0].item["transition_in"] != nil {
			return nil, fmt.Errorf("%s: the first scene has no transition_in", p.Scenes[0].ID)
		}
	}

	if !hasSlides {
		return nil, fmt.Errorf("composition must include a slide section")
	}

	if len(p.Scenes) == 0 {
		return nil, fmt.Errorf("composition has no scenes")
	}

	if object(sections[len(sections)-1])["transition_out"] != nil {
		return nil, fmt.Errorf("transition on final section")
	}
	if object(sections[0])["transition_in"] != nil {
		return nil, fmt.Errorf("transition_in on first section")
	}

	resolver := newTransitionResolver(v, t, adv)
	var boundaries []*boundaryPlan
	for i := 0; i < len(p.Scenes)-1; i++ {
		b, err := resolver.boundary(p.Scenes[i], p.Scenes[i+1], i, sceneTr[i].out, sceneTr[i+1].in)
		if err != nil {
			return nil, fmt.Errorf("%s → %s: %w", p.Scenes[i].ID, p.Scenes[i+1].ID, err)
		}
		if b == nil || b.transition.Type == "cut" && len(b.out)+len(b.in)+len(b.shared) == 0 {
			continue
		}
		boundaries = append(boundaries, b)
		p.Transitions = append(p.Transitions, b.transition)
	}

	for i := range p.Scenes {
		if i > 0 {
			p.Scenes[i].Start = p.Scenes[i-1].Start + p.Scenes[i-1].Duration
			for _, tr := range p.Transitions {
				if tr.FromScene == i-1 {
					p.Scenes[i].Start -= tr.Duration
				}
			}
		}

		p.Duration = p.Scenes[i].Start + p.Scenes[i].Duration
	}

	for i := range p.Transitions {
		p.Transitions[i].Offset = p.Scenes[p.Transitions[i].FromScene+1].Start
	}
	if err := placeTransitionEffects(p, boundaries); err != nil {
		return nil, err
	}

	if music := object(assets["music"]); len(music) > 0 {
		source, err := mediaPath(base, music["path"], "audio")
		if err != nil {
			return nil, err
		}

		fadeIn, err := duration(choose(music["fade_in"], audioDefaults["fade_in"]))
		if err != nil {
			return nil, err
		}

		fadeOut, err := duration(choose(music["fade_out"], audioDefaults["fade_out"]))
		if err != nil {
			return nil, err
		}
		if fadeOut > p.Duration || fadeIn > p.Duration {
			return nil, fmt.Errorf("music fade exceeds video duration")
		}

		volume := num(choose(music["volume"], audioDefaults["default_volume"], json.Number("1")))
		p.Music = &MusicPlan{Path: source, Volume: volume, FadeIn: fadeIn, FadeOut: fadeOut, Normalize: audioDefaults["normalize"] != false}
		if duck := object(audioDefaults["ducking"]); flag(duck["enabled"]) {
			attack, err := duration(choose(duck["attack"], "0.15s"))
			if err != nil {
				return nil, fmt.Errorf("music ducking attack: %w", err)
			}

			release, err := duration(choose(duck["release"], "0.4s"))
			if err != nil {
				return nil, fmt.Errorf("music ducking release: %w", err)
			}

			p.Music.Ducking = &DuckingPlan{Enabled: true, Amount: num(choose(duck["amount"], json.Number("0.65"))), Attack: attack, Release: release}
		}
	}

	if err := applyAdvanced(adv); err != nil {
		return nil, fmt.Errorf("advanced: %w", err)
	}
	// Element clips may have animated or modified classic text.
	for i := range p.Scenes {
		if p.Scenes[i].ASS != "" {
			p.Scenes[i].ASS = v2ASSDocument(p.Video, p.Scenes[i])
		}
	}
	// Transition sides render above every other track of their scenes.
	for _, b := range boundaries {
		from, to := &p.Scenes[b.transition.FromScene], &p.Scenes[b.transition.FromScene+1]
		if len(b.out) > 0 {
			from.Tracks = append(from.Tracks, TrackPlan{ID: "transition-out", Clips: b.out})
		}
		if len(b.in) > 0 {
			to.Tracks = append(to.Tracks, TrackPlan{ID: "transition-in", Clips: b.in})
		}
	}

	return p, nil
}

func audioLayer(track, defaults obj, base string, at, remaining time.Duration) (AudioLayerPlan, error) {
	source, err := mediaPath(base, track["path"], "audio")
	if err != nil {
		return AudioLayerPlan{}, err
	}

	fi, err := duration(choose(track["fade_in"], defaults["fade_in"]))
	if err != nil {
		return AudioLayerPlan{}, err
	}

	fo, err := duration(choose(track["fade_out"], defaults["fade_out"]))
	if err != nil {
		return AudioLayerPlan{}, err
	}
	if fi > remaining || fo > remaining {
		return AudioLayerPlan{}, fmt.Errorf("audio fade exceeds remaining scene duration")
	}

	return AudioLayerPlan{Path: source, Volume: num(choose(track["volume"], defaults["default_volume"], json.Number("1"))), Offset: at, Duration: remaining, FadeIn: fi, FadeOut: fo}, nil
}

func bounds(el obj, video VideoSpec, w, h int) (int, int) {
	place := object(el["placement"])
	parts := strings.Split(str(place["anchor"]), "-")
	anchor := str(place["anchor"])
	x, y := 0, 0
	if anchor == "center" {
		x = (video.Width - w) / 2
		y = (video.Height - h) / 2
	} else {
		switch parts[len(parts)-1] {
		case "center":
			x = (video.Width - w) / 2
		case "right":
			x = video.Width - w
		}
		switch parts[0] {
		case "center":
			y = (video.Height - h) / 2
		case "bottom":
			y = video.Height - h
		}
	}
	return x + int(math.Round(num(place["offset_x"]))), y + int(math.Round(num(place["offset_y"])))
}

func effects(el obj, kind string) ([]EffectPlan, error) {
	var result []EffectPlan
	arr, _ := el["effects"].([]any)
	for _, raw := range arr {
		v := object(raw)
		typ := str(v["type"])
		text := typ == "text_shadow" || typ == "text_glow" || typ == "text_outline" || typ == "glass_panel"
		image := typ == "pan_zoom" || typ == "color_filter" || typ == "vignette" || typ == "blur"
		if text && kind != "text" || image && kind != "image" {
			return nil, fmt.Errorf("effect %s is not applicable to %s", typ, kind)
		}
		if text && typ != "glass_panel" && v["color"] != nil && !v2ASSColorSupported(str(v["color"])) {
			return nil, fmt.Errorf("effect %s color %q unsupported by ASS renderer", typ, v["color"])
		}

		if typ == "vignette" && str(v["color"]) != "" && str(v["color"]) != "#000000" && str(v["color"]) != "black" {
			return nil, fmt.Errorf("colored vignette is unsupported by render.Plan")
		}
		fx := EffectPlan{Type: typ, Color: str(v["color"]), Opacity: num(choose(v["opacity"], jsonNumber(1))), OffsetX: num(v["offset_x"]), OffsetY: num(v["offset_y"]), Blur: num(v["blur"]), Width: num(v["width"]), FromScale: num(choose(v["from_scale"], jsonNumber(1))), ToScale: num(choose(v["to_scale"], jsonNumber(1))), FromAnchor: str(choose(v["from_anchor"], "center")), ToAnchor: str(choose(v["to_anchor"], "center")), Preset: str(v["preset"]), Intensity: num(choose(v["intensity"], jsonNumber(1))), Radius: num(v["radius"])}
		if padding, ok := v["padding"].([]any); ok {
			fx.PaddingX = num(padding[0])
			fx.PaddingY = num(padding[1])
		} else {
			fx.PaddingX = num(v["padding"])
			fx.PaddingY = fx.PaddingX
		}
		var e error
		fx.FadeIn, e = duration(v["fade_in"])
		if e != nil {
			return nil, e
		}
		fx.FadeOut, e = duration(v["fade_out"])
		if e != nil {
			return nil, e
		}
		result = append(result, fx)
	}
	return result, nil
}

func mediaLayer(source string, el, defaults obj, video VideoSpec) (LayerPlan, error) {
	w := int(math.Round(float64(video.Width) * ratio(el["max_width_ratio"])))
	h := int(math.Round(float64(video.Height) * ratio(el["max_height_ratio"])))
	if w < 1 || h < 1 {
		return LayerPlan{}, fmt.Errorf("media bounds empty")
	}

	x, y := bounds(el, video, w, h)
	fx, e := effects(el, "image")
	if e != nil {
		return LayerPlan{}, e
	}
	shape := str(el["shape"])

	if shape == "" {
		shape = "rectangle"
	}
	fitX, err := fitPosition(el, "left", "right")
	if err != nil {
		return LayerPlan{}, err
	}
	fitY, err := fitPosition(el, "top", "bottom")
	if err != nil {
		return LayerPlan{}, err
	}
	return LayerPlan{Kind: "image", Path: source, Fit: str(choose(el["fit"], defaults["default_fit"], "cover")), FitX: fitX, FitY: fitY, Anchor: str(object(el["placement"])["anchor"]), Shape: shape, X: x, Y: y, Width: w, Height: h, Opacity: 1, Effects: fx}, nil
}

func fitPosition(el obj, start, end string) (*float64, error) {
	if el[start] != nil && el[end] != nil {
		return nil, fmt.Errorf("specify only one of %s and %s", start, end)
	}
	if el[start] == nil && el[end] == nil {
		return nil, nil
	}
	value := num(choose(el[start], el[end]))
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return nil, fmt.Errorf("%s/%s fit position must be in [0,1]", start, end)
	}
	if el[end] != nil {
		value = 1 - value
	}
	return &value, nil
}

func textLayer(value, key string, el, typography, theme obj, video VideoSpec) (LayerPlan, error) {
	style := object(el["style"])
	size := integer(choose(style["font_size"], typography["body_size"], jsonNumber(32)))
	if key == "title" {
		size = integer(choose(style["font_size"], typography["title_size"], typography["body_size"], jsonNumber(48)))
	}
	if key == "tip" {
		size = integer(choose(style["font_size"], typography["tip_size"], typography["body_size"], jsonNumber(28)))
	}
	if size <= 0 {
		return LayerPlan{}, fmt.Errorf("no font size configured")
	}
	if spacing := num(style["line_spacing"]); spacing != 0 {
		return LayerPlan{}, fmt.Errorf("line_spacing requires render.Plan support")
	}

	width := int(float64(video.Width) * ratio(el["max_width_ratio"]))
	if width < 1 {
		return LayerPlan{}, fmt.Errorf("text width empty")
	}

	font := str(choose(style["font_family"], typography["font_family"], "Arial"))
	weight, err := parseWeight(style["weight"])
	if err != nil {
		return LayerPlan{}, err
	}
	face := fontsOf(video).face(font, weight, float64(size))
	letter, word := num(style["letter_spacing"]), num(style["word_spacing"])
	fits := func(line string) bool {
		return face.lineWidth([]rune(line), float64(size), letter, word) <= float64(width)
	}

	// Explicit line breaks are kept; each paragraph is wrapped on its own, at
	// the widths its font measures. A paragraph that fits is kept verbatim,
	// spaces included, so indented code stays indented and texts layered over
	// each other keep the same character positions; a wrapped one keeps its
	// indentation on its first line.
	var lines []string
	for paragraph := range strings.SplitSeq(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		paragraph = strings.ReplaceAll(paragraph, "\t", "    ")
		if fits(paragraph) {
			lines = append(lines, paragraph)
			continue
		}
		line := paragraph[:len(paragraph)-len(strings.TrimLeft(paragraph, " "))]
		words := 0
		for word := range strings.FieldsSeq(paragraph) {
			if words > 0 && !fits(line+" "+word) {
				lines = append(lines, line)
				line, words = "", 0
			}
			if words > 0 {
				line += " "
			}
			line += word
			words++
		}
		lines = append(lines, line)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if maxLines := integer(el["max_lines"]); maxLines > 0 && len(lines) > maxLines {
		return LayerPlan{}, fmt.Errorf("text exceeds max_lines %d", maxLines)
	}

	height := int(math.Ceil(float64(len(lines)*size) * 1.2))
	x, y := bounds(el, video, width, height)
	fx, err := effects(el, "text")
	if err != nil {
		return LayerPlan{}, err
	}

	color := str(choose(style["color"], theme["text_color"]))
	if !v2ASSColorSupported(color) {
		return LayerPlan{}, fmt.Errorf("text color %q is unsupported by ASS renderer", color)
	}

	return LayerPlan{Kind: "text", X: x, Y: y, Width: width, Height: height, WrapWidth: width, Font: face.Name, Weight: face.Weight, face: face, FontSize: size, Color: color, Opacity: 1, Spans: []TextSpan{{Content: strings.Join(lines, "\n"), Color: color, Bold: weight >= 600}}, Effects: fx,
		LetterSpacing: letter, WordSpacing: word}, nil
}

func ratio(v any) float64 {
	if v == nil {
		return 1
	}
	return num(v)
}

func addProgress(scene *ScenePlan, el obj, current, total int, theme obj, video VideoSpec) error {
	if el["visible"] == false {
		return nil
	}
	w := int(float64(video.Width) * ratio(choose(el["width_ratio"], jsonNumber(0.8))))
	h := int(math.Round(num(choose(el["thickness"], jsonNumber(8)))))
	if w < 1 || h < 1 {
		return fmt.Errorf("progress dimensions empty")
	}
	x, y := bounds(el, video, w, h)
	background := str(choose(el["background_color"], theme["muted_color"], theme["background"]))
	color := str(choose(el["color"], theme["accent_color"]))
	scene.Layers = append(scene.Layers, LayerPlan{Kind: "rectangle", X: x, Y: y, Width: w, Height: h, Color: background, Opacity: 1}, LayerPlan{Kind: "rectangle", X: x, Y: y, Width: max(1, int(math.Round(float64(w)*float64(current)/float64(total)))), Height: h, Color: color, Opacity: 1})
	return nil
}

func jsonNumber(n float64) any { return n }
