package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
	"github.com/go-text/typesetting/fontscan"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"
)

// Text is measured with the font file libass draws it with, so wrapping,
// boxes, highlights, and carets fit the glyphs. A family resolves, in order,
// to a font in the project's font directories, an installed font, a font
// downloaded from Google Fonts (cached), or an explicitly named bundled Go font. Fonts that
// aren't installed reach libass through the render's fonts directory, under
// the family name the ASS documents then use.

// fontFace is one resolved font, ready to measure.
type fontFace struct {
	// Name is the family written into ASS documents (\fn) and Weight the
	// weight (\b): what makes libass pick this face.
	Name   string
	Weight int
	Path   string
	// Bundled fonts aren't installed: libass loads them from the render's
	// fonts directory.
	Bundled bool
	face    *font.Face
	upem    float64
	// height is usWinAscent+usWinDescent in font units: libass sizes fonts so
	// that it equals \fs, which is also the line height.
	height   float64
	ascent   float64
	variable bool
}

type fontKey struct {
	family string
	weight int
	opsz   int // the size of the text, for optical sizes
}

type loadKey struct {
	loc    font.FontID
	weight int
	vary   bool
}

// fontBook resolves and caches font faces. It is safe for concurrent use.
type fontBook struct {
	dirs []string // project font files and directories

	mu      sync.Mutex
	faces   map[fontKey]*fontFace
	loaded  map[loadKey]*fontFace
	project []fontscan.Footprint // scanned on first use
	scanned bool
	system  []fontscan.Footprint
	sysOnce sync.Once
}

// Faces are shared between books and plans, and neither they nor the shaper
// are safe for concurrent use: shaping takes turns.
var (
	shapingMu sync.Mutex
	shaper    shaping.HarfbuzzShaper
)

func newFontBook(dirs []string) *fontBook {
	return &fontBook{dirs: dirs, faces: map[fontKey]*fontFace{}, loaded: map[loadKey]*fontFace{}}
}

// books shares one font book between plans with the same font directories,
// so fonts resolve once per process.
var books sync.Map

func fontBookFor(dirs []string) *fontBook {
	key := strings.Join(dirs, "\x00")
	if b, ok := books.Load(key); ok {
		return b.(*fontBook)
	}
	b, _ := books.LoadOrStore(key, newFontBook(dirs))
	return b.(*fontBook)
}

// defaultFonts serves layers compiled without a project font book.
var defaultFonts = newFontBook(nil)

// fontsOf returns the plan's font book.
func fontsOf(video VideoSpec) *fontBook {
	if video.fonts != nil {
		return video.fonts
	}
	return defaultFonts
}

// warnf reports a recoverable problem once.
var (
	warned   sync.Map
	warnings io.Writer = os.Stderr
)

func warnf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if _, seen := warned.LoadOrStore(message, true); !seen {
		fmt.Fprintf(warnings, "\x1b[33m[Warning] %s\x1b[0m\n", message)
	}
}

// fontWeights are the weight names text styles accept, as in CSS.
var fontWeights = map[string]int{
	"thin": 100, "extralight": 200, "light": 300, "normal": 400, "regular": 400,
	"medium": 500, "semibold": 600, "bold": 700, "extrabold": 800, "black": 900,
}

// parseWeight reads a text style's weight: a name or a number from 1 to 1000.
func parseWeight(v any) (int, error) {
	if v == nil {
		return 400, nil
	}
	if n, ok := number(v); ok {
		if n < 1 || n > 1000 || n != math.Trunc(n) {
			return 0, fmt.Errorf("font weight %v must be a whole number from 1 to 1000", v)
		}
		return int(n), nil
	}
	if w, ok := fontWeights[strings.ToLower(str(v))]; ok {
		return w, nil
	}
	return 0, fmt.Errorf("unknown font weight %q", v)
}

// face resolves family at weight (as in CSS: 400 regular, 700 bold) for text
// drawn at size pixels, which picks optical sizes where the font has them; a
// size of 0 uses the font's default. Unresolved families return an error.
func (b *fontBook) face(family string, weight int, size float64) (*fontFace, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := fontKey{font.NormalizeFamily(family), weight, int(math.Round(size))}
	if f := b.faces[key]; f != nil {
		return f, nil
	}
	f, err := b.resolve(family, weight, size)
	if err != nil {
		return nil, err
	}
	b.faces[key] = f
	return f, nil
}

func (b *fontBook) resolve(family string, weight int, size float64) (*fontFace, error) {
	if !b.scanned {
		b.project = scanFontFiles(b.dirs)
		b.scanned = true
	}
	// Project fonts reach libass as files. Variable fonts, project or
	// installed, are instanced at the weight and size (fontinstance.go).
	if loc, ok := bestFootprint(b.project, family, weight); ok {
		if f, err := b.load(loc, family, weight, false); err == nil {
			if f.variable {
				if inst, err := b.instance(loc, family, weight, size); err == nil {
					return inst, nil
				}
			}
			f.Bundled = true
			return f, nil
		}
	}
	// Static installed fonts keep their name, and libass picks the weight.
	if loc, ok := bestFootprint(b.systemFonts(), family, weight); ok {
		if f, err := b.load(loc, family, weight, true); err == nil {
			if f.variable {
				if inst, err := b.instance(loc, family, weight, size); err == nil {
					return inst, nil
				}
			}
			f.Name, f.Weight = family, weight
			return f, nil
		}
	}
	// Bundled families are available by their real names, never as substitutes.
	if name := font.NormalizeFamily(family); name == font.NormalizeFamily("Go") || name == font.NormalizeFamily("Go Mono") {
		return fallbackFace(family, weight), nil
	}
	path, err := googleFont(family, weight, size)
	if err == nil {
		var f *fontFace
		f, err = b.load(font.FontID{File: path}, family, weight, false)
		if err == nil {
			f.Bundled = true
			return f, nil
		}
	}
	googleMu.Lock()
	_, listed := googleFamilies[font.NormalizeFamily(family)]
	googleMu.Unlock()
	if listed {
		return nil, fmt.Errorf("font %q is listed on Google Fonts but could not be loaded: %w", family, err)
	}
	return nil, b.missingFontError(family, err)
}

// load opens one face of a font file, once. With vary, a variable font is set
// to weight; otherwise it keeps its default instance.
func (b *fontBook) load(loc font.FontID, family string, weight int, vary bool) (*fontFace, error) {
	key := loadKey{loc, weight, vary}
	if !vary {
		key.weight = 0
	}
	if f := b.loaded[key]; f != nil {
		copy := *f
		return &copy, nil
	}
	f, err := loadFace(loc, family, weight, vary)
	if err != nil {
		return nil, err
	}
	b.loaded[key] = f
	copy := *f
	return &copy, nil
}

// instance is instancedFace, with failures reported once: the font is then
// drawn at the instance libass picks, which can differ from the measurement.
func (b *fontBook) instance(loc font.FontID, family string, weight int, size float64) (*fontFace, error) {
	f, err := instancedFace(loc, family, weight, size)
	if err != nil {
		warnf("variable font %s can't be instanced (%v); its weights may not match", loc.File, err)
	}
	return f, err
}

// systemFonts are the installed fonts, unless YAML2VIDEO_SYSTEM_FONTS=off,
// which makes renders independent of the machine's fonts.
func (b *fontBook) systemFonts() []fontscan.Footprint {
	if os.Getenv("YAML2VIDEO_SYSTEM_FONTS") == "off" {
		return nil
	}
	b.sysOnce.Do(func() {
		b.system, _ = fontscan.SystemFonts(log.New(io.Discard, "", 0), fontCacheDir("index"))
	})
	return b.system
}

// bestFootprint picks the face of family closest to weight, upright first.
func bestFootprint(prints []fontscan.Footprint, family string, weight int) (font.FontID, bool) {
	want := font.NormalizeFamily(family)
	best, score := font.FontID{}, math.Inf(1)
	for _, fp := range prints {
		if fp.Family != want {
			continue
		}
		s := math.Abs(float64(fp.Aspect.Weight) - float64(weight))
		if fp.Aspect.Style != font.StyleNormal {
			s += 1000
		}
		if fp.Aspect.Stretch != font.StretchNormal {
			s += 500
		}
		if s < score {
			best, score = fp.Location, s
		}
	}
	return best, !math.IsInf(score, 1)
}

var fontExtensions = []string{".ttf", ".otf", ".ttc", ".otc"}

// scanFontFiles indexes the font files in dirs (files or directories).
func scanFontFiles(dirs []string) []fontscan.Footprint {
	var out []fontscan.Footprint
	for _, root := range dirs {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !slices.Contains(fontExtensions, strings.ToLower(filepath.Ext(path))) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			loaders, err := ot.NewLoaders(bytes.NewReader(data))
			if err != nil {
				return nil
			}
			for i, ld := range loaders {
				desc, _ := font.Describe(ld, nil)
				out = append(out, fontscan.Footprint{
					Location: font.FontID{File: path, Index: uint16(i)},
					Family:   font.NormalizeFamily(desc.Family),
					Aspect:   desc.Aspect,
				})
			}
			return nil
		})
	}
	return out
}

// loadFace opens one face of a font file, named after its family name record
// (as libass sees it) and weighted by its OS/2 weight class.
func loadFace(loc font.FontID, family string, weight int, vary bool) (*fontFace, error) {
	data, err := os.ReadFile(loc.File)
	if err != nil {
		return nil, err
	}
	loaders, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if int(loc.Index) >= len(loaders) {
		return nil, fmt.Errorf("%s has no face %d", loc.File, loc.Index)
	}
	f, err := newFace(loaders[loc.Index], weight, vary)
	if err != nil {
		return nil, err
	}
	f.Path = loc.File
	if f.Name == "" {
		f.Name = family
	}
	return f, nil
}

func newFace(ld *ot.Loader, weight int, vary bool) (*fontFace, error) {
	ft, err := font.NewFont(ld)
	if err != nil {
		return nil, err
	}
	face := font.NewFace(ft)
	variable := ld.HasTable(ot.MustNewTag("fvar"))
	if vary && variable {
		face.SetVariations([]font.Variation{{Tag: ot.MustNewTag("wght"), Value: float32(weight)}})
	}
	f := &fontFace{face: face, upem: float64(ft.Upem()), Name: familyName(ld), Weight: 400, variable: variable}
	// libass's VSFilter-compatible sizing: \fs is the OS/2 win height.
	if os2, err := ld.RawTable(ot.MustNewTag("OS/2")); err == nil && len(os2) >= 78 {
		f.Weight = int(binary.BigEndian.Uint16(os2[4:]))
		ascent, descent := binary.BigEndian.Uint16(os2[74:]), binary.BigEndian.Uint16(os2[76:])
		f.height, f.ascent = float64(ascent)+float64(descent), float64(ascent)
	}
	if f.height == 0 {
		if ext, ok := face.FontHExtents(); ok {
			f.height, f.ascent = float64(ext.Ascender-ext.Descender), float64(ext.Ascender)
		}
	}
	if f.height <= 0 {
		f.height, f.ascent = f.upem, f.upem*0.8
	}
	return f, nil
}

// familyName reads a font's family name record (name ID 1), the name libass
// matches \fn against.
func familyName(ld *ot.Loader) string {
	raw, err := ld.RawTable(ot.MustNewTag("name"))
	if err != nil {
		return ""
	}
	names, _, err := tables.ParseName(raw)
	if err != nil {
		return ""
	}
	return names.Name(1)
}

// fallbackFace is a bundled Go font: Go Mono for families that look
// monospaced, Go otherwise, bold from weight 600.
func fallbackFace(family string, weight int) *fontFace {
	name := strings.ToLower(family)
	mono := false
	for _, hint := range []string{"mono", "code", "courier", "consol", "menlo", "terminal"} {
		mono = mono || strings.Contains(name, hint)
	}
	bold := weight >= 600
	data, file := goregular.TTF, "Go-Regular.ttf"
	switch {
	case mono && bold:
		data, file = gomonobold.TTF, "Go-Mono-Bold.ttf"
	case mono:
		data, file = gomono.TTF, "Go-Mono.ttf"
	case bold:
		data, file = gobold.TTF, "Go-Bold.ttf"
	}
	path := filepath.Join(fontCacheDir("bundled"), file)
	if _, err := os.Stat(path); err != nil {
		_ = writeAtomic(path, data)
	}
	ld, err := ot.NewLoader(bytes.NewReader(data))
	if err != nil {
		panic(err) // bundled fonts parse
	}
	f, err := newFace(ld, weight, false)
	if err != nil {
		panic(err)
	}
	f.Path, f.Bundled = path, true
	return f
}

// fontCacheDir is a directory for downloaded fonts and the system font index:
// in YAML2VIDEO_CACHE_DIR, or the user's cache directory.
func fontCacheDir(sub string) string {
	base := os.Getenv("YAML2VIDEO_CACHE_DIR")
	if base == "" {
		user, err := os.UserCacheDir()
		if err != nil {
			user = os.TempDir()
		}
		base = filepath.Join(user, "yaml2video")
	}
	dir := filepath.Join(base, "fonts", sub)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// pxPerUnit converts font units to pixels at font size size.
func (f *fontFace) pxPerUnit(size float64) float64 { return size / f.height }

// offsets lays out one line as libass does and returns the x of every
// character's left edge (in pixels from the line's start), followed by the
// line's end. The line is shaped as one run, kerned, with letter spacing
// after every character and word spacing after spaces; spaces count as
// spacing only where textBody keeps them as plain spaces.
func (f *fontFace) offsets(line []rune, size, letter, word float64) []float64 {
	out := make([]float64, len(line)+1)
	if len(line) == 0 {
		return out
	}
	text := make([]rune, len(line))
	plain := make([]bool, len(line)) // a space that gets word spacing
	for i, r := range line {
		text[i] = r
		if r == ' ' {
			if i == 0 || line[i-1] == ' ' {
				text[i] = ' ' // \h
			} else {
				plain[i] = true
			}
		}
	}
	var features []shaping.FontFeature
	if letter != 0 {
		// libass turns off ligatures in letter-spaced runs.
		features = []shaping.FontFeature{{Tag: ot.MustNewTag("liga"), Value: 0}, {Tag: ot.MustNewTag("clig"), Value: 0}}
	}
	// Shaped in font units (an em of upem pixels) and scaled after, so the
	// shaper's whole-pixel em rounding doesn't creep into the advances.
	shapingMu.Lock()
	shaped := shaper.Shape(shaping.Input{
		Text: text, RunStart: 0, RunEnd: len(text), Direction: di.DirectionLTR, Face: f.face,
		Size: fixed.I(int(f.upem)), Script: language.LookupScript(scriptRune(text)),
		Language: language.NewLanguage("en"), FontFeatures: features,
	})
	// The output's glyphs are reused by the next call: read them now.
	px := f.pxPerUnit(size)
	advance := make([]float64, len(line))
	for _, g := range shaped.Glyphs {
		runes := max(g.RunesCount(), 1)
		share := float64(g.Advance) / 64 * px / float64(runes)
		for k := 0; k < runes && g.TextIndex()+k < len(advance); k++ {
			advance[g.TextIndex()+k] += share
		}
	}
	shapingMu.Unlock()

	x := 0.0
	for i := range line {
		out[i] = x
		x += advance[i] + letter
		if plain[i] {
			x += word
		}
	}
	out[len(line)] = x
	return out
}

// scriptRune is the first letter of text, which picks the shaping script.
func scriptRune(text []rune) rune {
	for _, r := range text {
		if r > ' ' && r != ' ' {
			return r
		}
	}
	return 'a'
}

// lineWidth is a line's width as libass aligns it: trailing plain spaces
// don't count.
func (f *fontFace) lineWidth(line []rune, size, letter, word float64) float64 {
	x := f.offsets(line, size, letter, word)
	end := len(line)
	for end > 1 && line[end-1] == ' ' && line[end-2] != ' ' {
		end--
	}
	return x[end]
}
