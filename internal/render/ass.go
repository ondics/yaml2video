package render

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	assScriptInfo = "[Script Info]\n" +
		"ScriptType: v4.00+\n" +
		"Kerning: yes\n" +
		"PlayResX: %d\n" +
		"PlayResY: %d\n\n"

	assStyles = "[V4+ Styles]\n" +
		"Format: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\n" +
		"Style: Default,Arial,24,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,3,0,0,5,0,0,0,1\n\n"

	assEvents = "[Events]\n" +
		"Format: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text\n"
)

func assDocument(width, height int, scene ScenePlan) string {
	var document strings.Builder

	fmt.Fprintf(&document, assScriptInfo, width, height)
	document.WriteString(assStyles)
	document.WriteString(assEvents)

	for _, layer := range scene.Layers {
		if layer.Kind != "text" {
			continue
		}

		text := assSpans(
			layer.Spans,
			layer.Font,
			boldFont(layer.Font, layer.boldFace),
			layer.FontSize,
			layer.Color,
			layer.Opacity,
			nil,
			false,
		)
		tags, fadeIn, fadeOut := textEffectTags(layer.Effects)
		if tags != "" || fadeIn > 0 || fadeOut > 0 {
			prefix := fmt.Sprintf("{\\an5\\pos(%d,%d)%s", layer.X+layer.Width/2, layer.Y+layer.Height/2, tags)
			if fadeIn > 0 || fadeOut > 0 {
				prefix += fmt.Sprintf("\\fad(%d,%d)", fadeIn, fadeOut)
			}
			fmt.Fprintf(&document, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s}%s\n", assTime(0), assTime(scene.Duration), prefix, text)
		} else {
			writeASSDialogue(&document, 0, scene.Duration, 5, layer.X+layer.Width/2, layer.Y+layer.Height/2, text, nil, layer.Opacity, false, 0, 0)
		}
	}

	for _, subtitle := range scene.Subtitles {
		text := assSpans(
			subtitle.Spans,
			subtitle.Font,
			boldFont(subtitle.Font, subtitle.boldFace),
			subtitle.FontSize,
			subtitle.Color,
			1,
			subtitle.Background,
			true,
		)
		fadeIn, fadeOut := subtitleFadeDurations(subtitle)

		writeASSDialogue(
			&document,
			subtitle.Start,
			subtitle.End,
			2,
			subtitle.X,
			subtitle.Y,
			text,
			subtitle.Background,
			1,
			true,
			fadeIn,
			fadeOut,
		)
	}

	return document.String()
}

func writeASSDialogue(
	document *strings.Builder,
	start, end time.Duration,
	alignment, x, y int,
	text string,
	background *TextBackgroundPlan,
	backgroundOpacity float64,
	includeFade bool,
	fadeIn, fadeOut int,
) {
	overrides := fmt.Sprintf("{\\an%d\\pos(%d,%d)", alignment, x, y)
	overrides += assBackground(background, backgroundOpacity)
	if includeFade {
		overrides += fmt.Sprintf("\\fad(%d,%d)", fadeIn, fadeOut)
	}
	overrides += "}"

	fmt.Fprintf(
		document,
		"Dialogue: 0,%s,%s,Default,,0,0,0,,%s%s\n",
		assTime(start),
		assTime(end),
		overrides,
		text,
	)
}

func subtitleFadeDurations(subtitle SubtitlePlan) (fadeIn, fadeOut int) {
	if subtitle.In != nil && subtitle.In.Type == "fade" {
		fadeIn = int(subtitle.In.Duration.Milliseconds())
	}
	if subtitle.Out != nil && subtitle.Out.Type == "fade" {
		fadeOut = int(subtitle.Out.Duration.Milliseconds())
	}

	return fadeIn, fadeOut
}

// boldFont is the family libass draws bold spans with: the resolved bold face,
// which can be an instance named apart from the regular one, or font for
// plans built without one.
func boldFont(font string, bold *fontFace) string {
	if bold == nil {
		return font
	}
	return bold.Name
}

func assSpans(
	spans []TextSpan,
	font, boldFont string,
	size int,
	defaultColor string,
	opacity float64,
	background *TextBackgroundPlan,
	includeItemBackground bool,
) string {
	var value strings.Builder
	activeBackground := background

	for _, span := range spans {
		color := span.Color
		if color == "" {
			color = defaultColor
		}

		spanBackground := background
		if includeItemBackground && span.Background != nil {
			spanBackground = span.Background
		}
		if !includeItemBackground {
			spanBackground = nil
		}

		backgroundTags := ""
		if spanBackground != activeBackground {
			backgroundTags = assBackground(spanBackground, opacity)
			activeBackground = spanBackground
		}

		spanFont := font
		if span.Bold {
			spanFont = boldFont
		}
		fmt.Fprintf(
			&value,
			"{\\fn%s\\fs%d\\c%s\\1a%s\\b%d\\i%d\\u%d%s}%s",
			assEscape(spanFont),
			size,
			assColor(color),
			assAlpha(color, opacity),
			boolInt(span.Bold),
			boolInt(span.Italic),
			boolInt(span.Underline),
			backgroundTags,
			assEscape(span.Content),
		)
	}

	return value.String()
}

func assBackground(background *TextBackgroundPlan, layerOpacity float64) string {
	if background == nil {
		return "\\xbord0\\ybord0\\3a&HFF&"
	}

	return fmt.Sprintf(
		"\\3c%s\\3a%s\\xbord%d\\ybord%d",
		assColor(background.Color),
		assAlpha(background.Color, background.Opacity*layerOpacity),
		background.PaddingX,
		background.PaddingY,
	)
}

func assColor(color string) string {
	value := strings.TrimPrefix(strings.Split(color, "@")[0], "0x")
	if len(value) != 6 {
		return "&HFFFFFF&"
	}

	return "&H00" + value[4:6] + value[2:4] + value[0:2] + "&"
}

func assAlpha(color string, opacity float64) string {
	alpha := 1.0
	if _, after, ok := strings.Cut(color, "@"); ok {
		alpha, _ = strconv.ParseFloat(after, 64)
	}
	alpha *= opacity

	return fmt.Sprintf("&H%02X&", int((1-alpha)*255+0.5))
}

func assEscape(value string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"{", "\\{",
		"}", "\\}",
		"\n", "\\N",
	).Replace(value)
}

func assTime(value time.Duration) string {
	centiseconds := value.Milliseconds() / 10

	return fmt.Sprintf(
		"%d:%02d:%02d.%02d",
		centiseconds/360000,
		(centiseconds/6000)%60,
		(centiseconds/100)%60,
		centiseconds%100,
	)
}

func boolInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

func v2ASSTime(d time.Duration) string {
	cs := int64(d / (10 * time.Millisecond))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}
func v2ASSAlpha(opacity float64) string     { return fmt.Sprintf("&H%02X&", int((1-opacity)*255+0.5)) }
func v2ASSColorSupported(color string) bool { return len(color) == 7 && strings.HasPrefix(color, "#") }
func v2ASSColor(color string) string {
	if strings.HasPrefix(color, "#") && len(color) == 7 {
		return "&H" + color[5:7] + color[3:5] + color[1:3] + "&"
	}
	return "&HFFFFFF&"
}

func v2ASSDocument(video VideoSpec, scene ScenePlan) string {
	var b strings.Builder
	animated := false
	for _, layer := range scene.Layers {
		animated = animated || layer.Kind == "text" && layer.Animation != nil
	}
	writeV2ASSHeader(&b, video, animated)
	for _, layer := range scene.Layers {
		if layer.Kind != "text" {
			continue
		}
		if layer.Animation != nil {
			writeAnimatedText(&b, layer, layer.Align, 0, scene.Duration, frameStep(video))
			continue
		}
		writeStaticText(&b, layer, layer.Align, "Default", 0, scene.Duration)
	}
	return b.String()
}

// The Default style draws borders as opaque boxes (BorderStyle 3), which v2
// text relies on. Advanced-mode text uses the Advanced style instead, where
// borders follow the glyphs, so text_glow and text_outline surround the letters.
const advancedASSStyle = "Style: Advanced,Arial,24,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1\n"

func writeV2ASSHeader(b *strings.Builder, video VideoSpec, advanced bool) {
	// WrapStyle 2: text arrives wrapped, so libass must not wrap it again, not
	// even when keyframes scale a line beyond the frame. Kerning is on, as in
	// the shaping that measures the text.
	fmt.Fprintf(b, "[Script Info]\nScriptType: v4.00+\nWrapStyle: 2\nKerning: yes\nPlayResX: %d\nPlayResY: %d\n\n[V4+ Styles]\nFormat: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding\nStyle: Default,Arial,24,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,3,0,0,5,0,0,0,1\n", video.Width, video.Height)
	if advanced {
		b.WriteString(advancedASSStyle)
	}
	b.WriteString("\n[Events]\nFormat: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text\n")
}

// v2TextParts returns the dialogue pieces shared by static and animated text:
// the \an alignment, anchor position, style and effect tags, fades, and escaped text.
func v2TextParts(layer LayerPlan, align string) (alignment, x, y int, style, tags string, fadeIn, fadeOut int, value string) {
	// \b takes a weight (100–900) as well as 0 and 1.
	bold := layer.Weight
	if bold == 0 && len(layer.Spans) > 0 && layer.Spans[0].Bold {
		bold = 1
	}
	font := strings.NewReplacer("{", "", "}", "", "\\", "").Replace(layer.Font)
	for _, fx := range layer.Effects {
		switch fx.Type {
		case "text_shadow":
			tags += fmt.Sprintf("\\4c%s\\4a%s\\xshad%g\\yshad%g\\blur%g", v2ASSColor(fx.Color), v2ASSAlpha(fx.Opacity), fx.OffsetX, fx.OffsetY, fx.Blur)
		case "text_outline", "text_glow":
			width := fx.Width
			if fx.Type == "text_glow" {
				width = math.Max(1, fx.Blur*2)
			}
			tags += fmt.Sprintf("\\3c%s\\3a%s\\bord%g\\blur%g", v2ASSColor(fx.Color), v2ASSAlpha(fx.Opacity), width, fx.Blur)
		case "fade":
			fadeIn, fadeOut = int(fx.FadeIn.Milliseconds()), int(fx.FadeOut.Milliseconds())
		}
	}
	alignment, x = 5, layer.X+layer.Width/2
	switch align {
	case "left":
		alignment, x = 4, layer.X
	case "right":
		alignment, x = 6, layer.X+layer.Width
	}
	style = fmt.Sprintf("\\fn%s\\fs%d\\c%s\\b%d", font, layer.FontSize, v2ASSColor(layer.Color), bold)
	if layer.LetterSpacing != 0 {
		style += fmt.Sprintf("\\fsp%g", layer.LetterSpacing)
	}
	value = textBody(layer, nil, "")
	return alignment, x, layer.Y + layer.Height/2, style, tags, fadeIn, fadeOut, value
}

// textBody escapes a text layer for ASS. Line breaks become \N, spaces at a
// line's start or after another space become hard spaces (libass drops them
// otherwise), and spaces get the layer's word spacing. Characters for which
// show returns false are drawn fully transparent, so they keep their place;
// restore sets the visible alpha again after them. A nil show shows all.
func textBody(layer LayerPlan, show func(char, line int) bool, restore string) string {
	var b strings.Builder
	hidden := false
	char, line := 0, 0
	previous := '\n'
	for _, span := range layer.Spans {
		for _, r := range span.Content {
			if r == '\n' {
				b.WriteString("\\N")
				line++
				previous = r
				continue
			}
			if visible := show == nil || show(char, line); visible == hidden {
				if visible {
					b.WriteString(restore)
				} else {
					b.WriteString("{\\alpha&HFF&}")
				}
				hidden = !visible
			}
			char++
			switch {
			case r == ' ' && (previous == ' ' || previous == '\n'):
				b.WriteString("\\h")
			case r == ' ' && layer.WordSpacing != 0:
				fmt.Fprintf(&b, "{\\fsp%g} {\\fsp%g}", layer.LetterSpacing+layer.WordSpacing, layer.LetterSpacing)
			case r == '\\':
				b.WriteString("\\\\")
			case r == '{':
				b.WriteString("\\{")
			case r == '}':
				b.WriteString("\\}")
			default:
				b.WriteRune(r)
			}
			previous = r
		}
	}
	return b.String()
}

// textCounts returns a text layer's number of characters (line breaks
// excluded) and lines, as the reveal counts them.
func textCounts(layer LayerPlan) (chars, lines int) {
	lines = 1
	for _, span := range layer.Spans {
		for _, r := range span.Content {
			if r == '\n' {
				lines++
			} else {
				chars++
			}
		}
	}
	return chars, lines
}

// revealAt returns which characters and lines of a text layer show at t (on
// the layer's timeline), or nil when all of them do: those inside the reveal
// and, with a match, covered by it. Counts between whole numbers (while
// animating) round down, so a character shows whole or not at all.
func revealAt(layer LayerPlan, t time.Duration) func(char, line int) bool {
	r := defaultReveal
	if layer.Reveal != nil {
		r = *layer.Reveal
	}
	chars, lines := textCounts(layer)
	count := func(name string, v RevealValue, total int) int { return revealCount(layer, name, v, total, t) }
	c0, c1 := count("chars_start", r.CharsStart, chars), count("chars_end", r.CharsEnd, chars)
	l0, l1 := count("lines_start", r.LinesStart, lines), count("lines_end", r.LinesEnd, lines)
	matched := matchedChars(layer)
	if c0 == 0 && c1 == chars && l0 == 0 && l1 == lines && matched == nil {
		return nil
	}
	return func(char, line int) bool {
		return char >= c0 && char < c1 && line >= l0 && line < l1 && (matched == nil || matched[char])
	}
}

// revealCount is a reveal property's count at t, its rest value v when it
// isn't animated, limited to 0–total. Counts between whole numbers (while
// animating) round down.
func revealCount(layer LayerPlan, name string, v RevealValue, total int, t time.Duration) int {
	n := layer.Animation.ValueOf(name, t, v.of(total), float64(total))
	return int(math.Floor(math.Max(0, math.Min(float64(total), n)) + 1e-9))
}

// matchedChars marks the characters (line breaks excluded) a text layer's
// match covers, or returns nil without a match.
func matchedChars(layer LayerPlan) []bool {
	if layer.Match == nil {
		return nil
	}
	return patternChars(layer, layer.Match)
}

// patternChars marks the characters (line breaks excluded) re's matches
// cover in a text layer: each match's first group, when re has groups.
func patternChars(layer LayerPlan, re *regexp.Regexp) []bool {
	content := spanText(layer)
	covered := make([]bool, len(content)) // by byte
	for _, m := range re.FindAllStringSubmatchIndex(content, -1) {
		from, to := m[0], m[1]
		if len(m) > 2 { // the first group, when the pattern has groups
			from, to = m[2], m[3]
		}
		for i := max(from, 0); i < to; i++ {
			covered[i] = true
		}
	}
	var chars []bool
	for i, r := range content {
		if r != '\n' {
			chars = append(chars, covered[i])
		}
	}
	return chars
}

func writeStaticText(b *strings.Builder, layer LayerPlan, align, assStyle string, start, end time.Duration) {
	alignment, x, y, style, tags, fadeIn, fadeOut, value := v2TextParts(layer, align)
	if show := revealAt(layer, 0); show != nil {
		value = textBody(layer, show, "{\\1a&H00&"+tags+"}")
	}
	if fadeIn > 0 || fadeOut > 0 {
		tags += fmt.Sprintf("\\fad(%d,%d)", fadeIn, fadeOut)
	}
	for _, span := range layer.visible(start, end) {
		fmt.Fprintf(b, "Dialogue: 0,%s,%s,%s,,0,0,0,,{\\an%d\\pos(%d,%d)%s%s}%s\n", v2ASSTime(span.Start), v2ASSTime(span.End), assStyle, alignment, x, y, style, tags, value)
	}
}
