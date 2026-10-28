package render

import (
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// Tests measure with the bundled Go fonts, whatever the machine has
// installed, and never download.
func TestMain(m *testing.M) {
	cache, err := os.MkdirTemp("", "yaml2video-test-cache-")
	if err != nil {
		panic(err)
	}
	os.Setenv("YAML2VIDEO_CACHE_DIR", cache)
	os.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	os.Setenv("YAML2VIDEO_OFFLINE", "1")
	warnings = io.Discard
	code := m.Run()
	os.RemoveAll(cache)
	os.Exit(code)
}

type testFontBook struct {
	*fontBook
	t *testing.T
}

func newTestFontBook(t *testing.T, dirs []string) testFontBook {
	return testFontBook{newFontBook(dirs), t}
}

func (b testFontBook) face(family string, weight int, size float64) *fontFace {
	b.t.Helper()
	f, err := b.fontBook.face(family, weight, size)
	if err != nil {
		b.t.Fatal(err)
	}
	return f
}

func TestBundledAndProjectFonts(t *testing.T) {
	b := newTestFontBook(t, nil)
	mono := b.face("Go Mono", 400, 0)
	if mono.Name != "Go Mono" || !mono.Bundled {
		t.Fatalf("fallback %q bundled=%v", mono.Name, mono.Bundled)
	}
	// A monospaced font advances evenly, kerning or not.
	x := mono.offsets([]rune("AVA ;"), 50, 0, 0)
	for i := 1; i < len(x); i++ {
		if math.Abs((x[i]-x[i-1])-x[1]) > 1e-9 {
			t.Fatalf("uneven advances %v", x)
		}
	}
	if f := b.face("Go", 700, 0); f.Name != "Go" || f.Weight != 600 { // Go Bold's weight class
		t.Error("Go should resolve to its bundled bold face")
	}

	// Font files in a project directory come first, named as libass sees them.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.ttf"), gomono.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newTestFontBook(t, []string{dir}).face("go mono", 400, 0)
	if f.Path != filepath.Join(dir, "mine.ttf") || f.Name != "Go Mono" || !f.Bundled {
		t.Errorf("project font %+v", f)
	}
}

func TestFontMeasurement(t *testing.T) {
	f := newTestFontBook(t, nil).face("Go", 400, 0)
	// Letter spacing follows every character, word spacing every plain space:
	// here the ones after a and b, not the leading or the repeated one (hard
	// spaces in ASS).
	plain := f.offsets([]rune(" a b  c"), 40, 0, 0)
	spaced := f.offsets([]rune(" a b  c"), 40, 2, 10)
	if got, want := spaced[7]-plain[7], 7*2.0+2*10; math.Abs(got-want) > 1e-9 {
		t.Errorf("spacing added %g, want %g", got, want)
	}
	// A trailing space doesn't count toward the width libass aligns.
	if f.lineWidth([]rune("ab "), 40, 0, 0) != f.offsets([]rune("ab"), 40, 0, 0)[2] {
		t.Error("trailing space counted")
	}
	// The line height is the font size: the win height maps to \fs.
	if math.Abs(f.pxPerUnit(50)*f.height-50) > 1e-9 {
		t.Error("win height must map to the font size")
	}
}

func TestParseWeight(t *testing.T) {
	for in, want := range map[any]int{nil: 400, "bold": 700, "SemiBold": 600, "black": 900, 650.0: 650} {
		if got, err := parseWeight(in); err != nil || got != want {
			t.Errorf("%v: %d, %v", in, got, err)
		}
	}
	for _, in := range []any{"heavyish", 0.0, 1001.0, 450.5} {
		if _, err := parseWeight(in); err == nil {
			t.Errorf("%v should be rejected", in)
		}
	}
}

func TestRenameFont(t *testing.T) {
	data, err := renameFont(goregular.TTF, "Go y2v 700 o48")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "renamed.ttf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := loadFace(font.FontID{File: path}, "x", 400, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "Go y2v 700 o48" {
		t.Errorf("name %q", f.Name)
	}
	// Only the name changed: it measures like the original.
	orig := newTestFontBook(t, nil).face("Go", 400, 0)
	if a, b := f.offsets([]rune("Rename"), 50, 0, 0), orig.offsets([]rune("Rename"), 50, 0, 0); a[6] != b[6] {
		t.Errorf("widths %g vs %g", a[6], b[6])
	}
	// The whole file sums to the magic number, as the head table says.
	if sum := sfntChecksum(data); sum != 0xB1B0AFBA {
		t.Errorf("file checksum %#x", sum)
	}
}

func TestInstanceFont(t *testing.T) {
	// Rebuilt from its outlines and advances, a font measures and names as
	// the instance it is.
	data, err := instanceFont(goregular.TTF, nil, "Go y2v 400")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "instance.ttf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := loadFace(font.FontID{File: path}, "x", 400, false)
	if err != nil {
		t.Fatal(err)
	}
	orig := newTestFontBook(t, nil).face("Go", 400, 0)
	text := []rune("Instanced, quickly: 1234")
	if a, b := f.offsets(text, 50, 0, 0), orig.offsets(text, 50, 0, 0); math.Abs(a[len(text)]-b[len(text)]) > 1e-6 {
		t.Errorf("width %g, original %g", a[len(text)], b[len(text)])
	}
	if f.Name != "Go y2v 400" || f.variable {
		t.Errorf("instance %q variable=%v", f.Name, f.variable)
	}
	// The outlines survive: the same points, as quadratic contours.
	gid, _ := orig.face.NominalGlyph('Q')
	before, _ := orig.face.GlyphDataOutline(gid)
	after, _ := f.face.GlyphDataOutline(gid)
	if len(after.Segments) == 0 || len(after.Segments) < len(before.Segments)-2 {
		t.Errorf("Q has %d segments, was %d", len(after.Segments), len(before.Segments))
	}
}

func TestEncodeGlyphCubics(t *testing.T) {
	// A quarter circle as one cubic: the quadratic pieces stay on the arc.
	k := float32(0.5523 * 1000)
	segments := []ot.Segment{
		{Op: ot.SegmentOpMoveTo, Args: [3]ot.SegmentPoint{{X: 1000, Y: 0}}},
		{Op: ot.SegmentOpCubeTo, Args: [3]ot.SegmentPoint{{X: 1000, Y: k}, {X: k, Y: 1000}, {X: 0, Y: 1000}}},
		{Op: ot.SegmentOpLineTo, Args: [3]ot.SegmentPoint{{X: 0, Y: 0}}},
	}
	glyph, box, points, contours := encodeGlyph(segments)
	if contours != 1 || points != 10 || box != [4]int{0, 0, 1000, 1000} || len(glyph) == 0 {
		t.Fatalf("contours %d points %d box %v", contours, points, box)
	}
	// On-curve points of the pieces lie on the circle.
	flags := glyph[10+2*contours+2:][:points]
	x, y := 0, 0
	xs, ys := glyph[10+2*contours+2+points:], glyph[10+2*contours+2+3*points:]
	for i := range points {
		x += int(int16(be16(xs[2*i:])))
		y += int(int16(be16(ys[2*i:])))
		if flags[i] == 1 && x > 0 && y > 0 {
			if r := math.Hypot(float64(x), float64(y)); math.Abs(r-1000) > 2 {
				t.Errorf("point %d (%d,%d) is %g from the center", i, x, y, r)
			}
		}
	}
}

func TestGoogleFontDownload(t *testing.T) {
	t.Setenv("YAML2VIDEO_OFFLINE", "")
	t.Setenv("YAML2VIDEO_CACHE_DIR", t.TempDir())
	resetGoogleFamilies(t)
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list":
			io.WriteString(w, `)]}'
{"familyMetadataList": [
  {"family": "Fake Sans", "axes": [{"tag": "opsz", "min": 12, "max": 96, "defaultValue": 14}, {"tag": "wght", "min": 200, "max": 800, "defaultValue": 400}], "fonts": {"400": {}}},
  {"family": "Fake Serif", "axes": [], "fonts": {"400": {}, "400i": {}, "700": {}}}
]}`)
		case "/css2":
			queries = append(queries, r.URL.Query().Get("family"))
			io.WriteString(w, "@font-face { src: url(http://"+r.Host+"/font.ttf) format('truetype'); }")
		case "/font.ttf":
			w.Write(goregular.TTF)
		}
	}))
	defer server.Close()
	oldCSS, oldList := googleCSS, googleList
	googleCSS, googleList = server.URL+"/css2", server.URL+"/list"
	defer func() { googleCSS, googleList = oldCSS, oldList }()

	// A variable family: the weight within its range, the optical size of the
	// text, each instance under a name of its own.
	b := newTestFontBook(t, nil)
	f := b.face("fake sans", 900, 120)
	if f.Name != "Fake Sans y2v 800 o96" || !f.Bundled || f.Weight != 400 {
		t.Errorf("instance %q weight %d bundled %v", f.Name, f.Weight, f.Bundled)
	}
	if b.face("Fake Sans", 650, 40).Name != "Fake Sans y2v 650 o40" {
		t.Error("each weight and size is its own instance")
	}
	// A static family: the nearest weight it has, without optical sizes.
	if got := b.face("Fake Serif", 900, 120).Name; got != "Fake Serif y2v 700" {
		t.Errorf("static instance %q", got)
	}
	want := []string{"Fake Sans:opsz,wght@96,800", "Fake Sans:opsz,wght@40,650", "Fake Serif:wght@700"}
	if strings.Join(queries, " | ") != strings.Join(want, " | ") {
		t.Errorf("queries %q", queries)
	}
	// Cached: a new book downloads nothing.
	queries = nil
	newTestFontBook(t, nil).face("Fake Sans", 900, 120)
	if len(queries) != 0 {
		t.Errorf("cached instance fetched again: %q", queries)
	}
	if _, err := googleFont("Nope", 400, 0); err != errNotOnGoogle {
		t.Errorf("got %v", err)
	}
	if f, err := b.fontBook.face("Fake San", 400, 0); err == nil || f != nil || !strings.Contains(err.Error(), `"Fake Sans" (Google Fonts)`) {
		t.Errorf("expected a Google Fonts suggestion without substitution, got %v, %v", f, err)
	}

	// Offline, in a new process: cached instances and the cached list, even
	// a stale one, are used; uncached instances aren't downloaded.
	t.Setenv("YAML2VIDEO_OFFLINE", "1")
	resetGoogleFamilies(t)
	old := time.Now().Add(-2 * googleListTTL)
	if err := os.Chtimes(filepath.Join(fontCacheDir("google"), "families.json"), old, old); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if got := newTestFontBook(t, nil).face("Fake Sans", 900, 120).Name; got != "Fake Sans y2v 800 o96" {
		t.Errorf("offline cached instance %q", got)
	}
	if _, err := newFontBook(nil).face("Fake Sans", 300, 120); !errors.Is(err, errOffline) {
		t.Errorf("offline uncached instance: %v", err)
	}
}

// resetGoogleFamilies forgets the in-memory family list, as a new process
// would, before and after a test.
func resetGoogleFamilies(t *testing.T) {
	reset := func() {
		googleMu.Lock()
		googleFamilies, googleListErr = nil, nil
		googleMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}
