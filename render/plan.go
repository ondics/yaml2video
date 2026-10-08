// Package render compiles a YAML project into deterministic FFmpeg work.
package render

import "time"

// Plan describes all intermediate scene renders and the final video render.
type Plan struct {
	Video       VideoSpec
	Scenes      []ScenePlan
	Transitions []BoundaryTransition
	Music       *MusicPlan

	Duration time.Duration
	Output   string
	WorkDir  string
}

// VideoSpec defines the output video properties shared by every scene.
type VideoSpec struct {
	Width      int
	Height     int
	FPS        int
	Background string
}

// ScenePlan describes the assets and timing needed to render one scene.
type ScenePlan struct {
	ID         string
	Start      time.Duration
	Duration   time.Duration
	Background string

	Layers    []LayerPlan
	Audio     []AudioLayerPlan
	Subtitles []SubtitlePlan

	ASS     string
	ASSPath string
	Output  string
}

// LayerPlan describes a visual layer within a scene.
type LayerPlan struct {
	Kind string
	Path string
	Fit  string
	// Anchor aligns contained media within its maximum-width/height box.
	// Empty retains centered positioning for plans without a placement anchor.
	Anchor string

	X      int
	Y      int
	Width  int
	Height int

	Opacity  float64
	Color    string
	Font     string
	FontSize int

	SourceOffset    time.Duration
	Duration        time.Duration // Zero uses the whole scene duration.
	Trim            *TrimPlan
	WrapWidth       int
	Spans           []TextSpan
	Background      *TextBackgroundPlan
	ItemBackgrounds []TextBackgroundBox
	// Effects are applied in order to this layer, not to the scene as a whole.
	Effects []EffectPlan
	Shape   string // "", "rectangle", "rounded-rectangle", or "circle" for media layers.
	// CornerRadius sets rounded-rectangle corner radius in pixels; zero uses 1/12 of the smaller dimension.
	CornerRadius int
	// AltText describes the image/video for accessibility; it is metadata only, not burnt into video.
	AltText string
}

// EffectPlan describes one v2 semantic effect. Fields not relevant to Type are ignored.
// Colors use FFmpeg color syntax (e.g. 0xRRGGBB or #RRGGBB).
type EffectPlan struct {
	Type       string
	Color      string
	Opacity    float64
	OffsetX    float64
	OffsetY    float64
	Blur       float64
	Width      float64
	PaddingX   float64
	PaddingY   float64
	FromScale  float64
	ToScale    float64
	FromAnchor string
	ToAnchor   string
	Preset     string
	Intensity  float64
	Radius     float64
	FadeIn     time.Duration
	FadeOut    time.Duration
}

// AudioLayerPlan describes audio within its scene.
type AudioLayerPlan struct {
	Path         string
	Volume       float64
	SourceOffset time.Duration
	Duration     time.Duration // Zero uses the remaining scene duration.
	Offset       time.Duration // Placement relative to scene start, independent of SourceOffset.
	FadeIn       time.Duration
	FadeOut      time.Duration
	SoundEffect  bool // Sound effects mix into the output but never trigger music ducking.
}

// TrimPlan describes fractional insets removed from a video's source frame.
type TrimPlan struct {
	Top    float64
	Bottom float64
	Left   float64
	Right  float64
}

// TextSpan is a styled portion of a text layer or subtitle.
type TextSpan struct {
	Content    string
	Color      string
	Bold       bool
	Italic     bool
	Underline  bool
	Background *TextBackgroundPlan
}

// TextBackgroundPlan describes the ASS outline used as a text background.
type TextBackgroundPlan struct {
	Color    string
	Opacity  float64
	PaddingX int
	PaddingY int
	Radius   int
}

// TextBackgroundBox is a static background rectangle for one text item.
type TextBackgroundBox struct {
	X          int
	Y          int
	Width      int
	Height     int
	Background TextBackgroundPlan
}

// SubtitlePlan describes a timed subtitle.
type SubtitlePlan struct {
	Spans    []TextSpan
	Font     string
	FontSize int
	Color    string

	X          int
	Y          int
	Start      time.Duration
	End        time.Duration
	In         *TransitionPlan
	Out        *TransitionPlan
	Background *TextBackgroundPlan
}

// TransitionPlan describes a transition applied to an item.
type TransitionPlan struct {
	Type     string
	Duration time.Duration
}

// BoundaryTransition describes a transition from one scene to the next.
type BoundaryTransition struct {
	FromScene int
	Type      string
	Duration  time.Duration
	Offset    time.Duration
}

// MusicPlan describes the looping background-music track.
type MusicPlan struct {
	Path      string
	Volume    float64
	FadeIn    time.Duration
	FadeOut   time.Duration
	Normalize bool // Enable loudnorm before applying Volume.
	Ducking   *DuckingPlan
}

// DuckingPlan reduces music while foreground audio is present.
type DuckingPlan struct {
	Enabled bool
	Amount  float64 // 0 means no reduction; 1 means maximum compression.
	Attack  time.Duration
	Release time.Duration
}
