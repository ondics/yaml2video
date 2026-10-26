// Package render compiles a YAML project into deterministic FFmpeg work.
package render

import (
	"regexp"
	"time"
)

// Plan describes all intermediate scene renders and the final video render.
type Plan struct {
	Video       VideoSpec
	Scenes      []ScenePlan
	Transitions []BoundaryTransition
	Music       *MusicPlan

	// Tracks are advanced-mode layers attached to the whole video. They stack,
	// in order, above the composed scenes and transitions.
	Tracks []TrackPlan
	// Audio holds advanced-mode audio clips placed on the whole video timeline.
	Audio []AudioLayerPlan
	// Master optionally normalizes the final audio mix (advanced mode).
	Master *MasterPlan

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
	// CRF is the x264 quality of the final encode (lower is better); zero uses defaultCRF.
	CRF int
	// fonts resolves the families text uses; nil uses defaultFonts.
	fonts *fontBook
}

// defaultCRF keeps flat graphics and small text free of visible artifacts.
const defaultCRF = 16

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

	// Role is the composition role (intro, slide, outro) that produced the scene.
	Role string
	// Layout names the template layout the scene uses.
	Layout string
	// Tracks are layers above the scene's classic layout: a custom layout's
	// elements first (track ID "layout"), then advanced-mode tracks.
	Tracks []TrackPlan

	item     obj
	index    int
	total    int
	bindings map[string]string
}

// TrackPlan is a stack of clips composited in order. Consecutive text clips
// share one ASS document, named ASSPrefix plus the run number, so text and
// media keep their exact stacking order.
type TrackPlan struct {
	ID        string
	Clips     []ClipPlan
	ASSPrefix string
	// Canvas is a folder's drawing area in frame coordinates: everything its
	// children reach, which may lie outside the frame. Nil for other tracks.
	Canvas *Rect
}

// Rect is an area in frame pixels.
type Rect struct {
	X, Y, W, H int
}

// Window is a time range on a layer's owner timeline.
type Window struct {
	Start, End time.Duration
}

// ClipPlan is a timed item on a track. Start is relative to the track's owner
// (its scene, or the video for video-attached tracks).
type ClipPlan struct {
	// ID names a custom layout element so tracks can target it.
	ID       string
	Kind     string // image, rect, circle, text, box, effect, or folder
	Start    time.Duration
	Duration time.Duration
	// Layer carries geometry, media, text, and the clip's own effects and keyframes.
	Layer LayerPlan
	// Adjustments are an effect clip's stacked filters, applied to everything below it.
	Adjustments []AdjustmentPlan
	// Mix blends an effect clip's result over its input (1 applies it fully).
	Mix float64
	// Folder holds a folder clip's children, timed from the folder's start.
	// They render onto a transparent canvas; the folder's Layer (keyframes,
	// opacity, pivot) and Adjustments then transform that canvas as one group.
	Folder *TrackPlan

	// The element definition and bindings, kept to compile modified variants.
	def      obj
	bindings map[string]string
}

// AdjustmentPlan is one filter of an effect clip. Fields not relevant to Type are ignored.
type AdjustmentPlan struct {
	Type       string
	Radius     float64
	Strength   float64
	Amount     float64
	Brightness float64
	Contrast   float64
	Saturation float64
	Gamma      float64
	Hue        float64
	Frequency  float64
	Color      string
	Opacity    float64
	Preset     string
	Intensity  float64
}

// LayerPlan describes a visual layer within a scene.
type LayerPlan struct {
	Kind string
	Path string
	Fit  string
	// Anchor aligns contained media within its maximum-width/height box.
	// Empty retains centered positioning for plans without a placement anchor.
	Anchor string
	// FitX/FitY override crop/padding alignment within the box (0=start, 1=end).
	// Nil preserves the existing centered crop or anchor-aligned padding.
	FitX, FitY *float64

	X      int
	Y      int
	Width  int
	Height int

	Opacity  float64
	Color    string
	Font     string
	FontSize int
	// Weight (100–900) picks the face of Font; zero uses the spans' bold.
	Weight int
	// face is the font Font resolved to, which measures the text.
	face *fontFace

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

	// Element names the template layout element ("title", "image", ...) this layer renders.
	Element string
	// Align is the text alignment used for ASS positioning.
	Align string
	// Animation holds advanced-mode keyframes relative to the layer's start.
	Animation *Animation
	// Pivot is the frame point that scale and rotation keyframes turn around;
	// nil uses the layer's center.
	Pivot *[2]float64
	// Reveal shows a fraction of a text's characters and lines; animated
	// reveal properties override these values.
	Reveal *RevealPlan
	// Match draws only the characters its matches cover (the first group's,
	// when it has groups); the others keep their space, like hidden ones.
	Match *regexp.Regexp
	// LetterSpacing adds pixels after every character, WordSpacing after every space.
	LetterSpacing float64
	WordSpacing   float64
	// ShowOnly limits the layer to one window; HideDuring removes it from
	// windows. Both are on the owner's timeline (element modifications).
	ShowOnly   *Window
	HideDuring []Window

	// The layout element and its content, kept to compile modified variants.
	def    obj
	source string
}

// RevealPlan selects the visible part of a text: the characters from
// CharsStart up to CharsEnd, on the lines from LinesStart up to LinesEnd.
// Hidden characters and lines keep their space.
type RevealPlan struct {
	CharsStart, CharsEnd, LinesStart, LinesEnd RevealValue
}

// RevealValue is a count of characters or lines, or with Relative a fraction
// of the text's total (written as a percentage).
type RevealValue struct {
	Value    float64
	Relative bool
}

// of resolves the value against a total.
func (v RevealValue) of(total int) float64 {
	if v.Relative {
		return v.Value * float64(total)
	}
	return v.Value
}

// defaultReveal shows everything.
var defaultReveal = RevealPlan{CharsEnd: RevealValue{1, true}, LinesEnd: RevealValue{1, true}}

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
	// VolumeAnimation multiplies Volume by keyframed values over the track's own time.
	VolumeAnimation *Animation
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

	// XFade overrides the xfade transition derived from Type (advanced
	// transitions); empty with a zero Duration is a cut. Expr is the
	// expression for XFade "custom".
	XFade string
	Expr  string
	// Effects are advanced-mode effect layers on the final timeline around the
	// boundary, with absolute start times.
	Effects []ClipPlan
}

// xfade returns the FFmpeg xfade transition, or "" for a cut.
func (t BoundaryTransition) xfade() string {
	if t.XFade != "" || t.Duration == 0 {
		return t.XFade
	}
	return transitions[t.Type]
}

// MusicPlan describes the looping background-music track.
type MusicPlan struct {
	Path      string
	Volume    float64
	FadeIn    time.Duration
	FadeOut   time.Duration
	Normalize bool // Bring the track to musicLoudness before applying Volume.
	Ducking   *DuckingPlan
	// GainDB is the measured normalization gain; Render sets it. Without it,
	// normalization falls back to loudnorm's dynamic mode.
	GainDB *float64
}

// MasterPlan sets the integrated loudness (LUFS) and true-peak ceiling (dBTP)
// of the final mix.
type MasterPlan struct {
	Loudness float64
	TruePeak float64
	// GainDB is the measured gain that brings the mix to Loudness; Render sets
	// it. Without it, the master falls back to loudnorm's dynamic mode, whose
	// gain rides the program (audible pumping in quiet passages).
	GainDB *float64
}

// DuckingPlan reduces music while foreground audio is present.
type DuckingPlan struct {
	Enabled bool
	Amount  float64 // 0 means no reduction; 1 means maximum compression.
	Attack  time.Duration
	Release time.Duration
}
