package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// libass can't set a variable font's axes: it draws a font file's default
// instance, or, for installed fonts, a named instance it picks itself. So
// variable fonts are instanced here instead, at exactly the weight a text
// asks for and (where the font has optical sizes) the text's size: a static
// TrueType file with outlines and advances at those axis values, under a
// family name of its own, which reaches libass like any bundled font.

type fontAxis struct {
	Tag               string
	Min, Default, Max float64
}

// fontAxes reads a variable font's axes from its fvar table.
func fontAxes(ld *ot.Loader) []fontAxis {
	raw, err := ld.RawTable(ot.MustNewTag("fvar"))
	if err != nil || len(raw) < 16 {
		return nil
	}
	offset, count, size := int(be16(raw[4:])), int(be16(raw[8:])), int(be16(raw[10:]))
	fixed := func(b []byte) float64 { return float64(int32(binary.BigEndian.Uint32(b))) / 65536 }
	var axes []fontAxis
	for i := range count {
		at := offset + i*size
		if size < 16 || at+16 > len(raw) {
			break
		}
		rec := raw[at:]
		axes = append(axes, fontAxis{string(rec[:4]), fixed(rec[4:]), fixed(rec[8:]), fixed(rec[12:])})
	}
	return axes
}

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }

// instanceAxes picks a variable font's axis values for weight and text of
// size pixels (0: the default optical size), and labels them for names.
func instanceAxes(axes []fontAxis, weight int, size float64) ([]font.Variation, string) {
	var vars []font.Variation
	label := ""
	for _, a := range axes {
		clamp := func(v float64) float64 { return math.Round(math.Max(a.Min, math.Min(a.Max, v))) }
		switch a.Tag {
		case "wght":
			w := clamp(float64(weight))
			vars = append(vars, font.Variation{Tag: ot.MustNewTag("wght"), Value: float32(w)})
			label += " " + strconv.Itoa(int(w))
		case "opsz":
			if size > 0 {
				o := clamp(size)
				vars = append(vars, font.Variation{Tag: ot.MustNewTag("opsz"), Value: float32(o)})
				label += " o" + strconv.Itoa(int(o))
			}
		}
	}
	return vars, label
}

// instancedFace returns the static instance of the variable font at loc for
// weight and size, cached, or an error when the font can't be instanced.
func instancedFace(loc font.FontID, family string, weight int, size float64) (*fontFace, error) {
	data, err := os.ReadFile(loc.File)
	if err != nil {
		return nil, err
	}
	if loc.Index != 0 || bytes.HasPrefix(data, []byte("ttcf")) {
		return nil, fmt.Errorf("font collections aren't instanced")
	}
	ld, err := ot.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	vars, label := instanceAxes(fontAxes(ld), weight, size)
	name := familyName(ld)
	if name == "" {
		name = family
	}
	name += " y2v" + label
	sum := sha256.Sum256(append(data, label...))
	path := filepath.Join(fontCacheDir("instances"), hex.EncodeToString(sum[:12])+".ttf")
	if _, err := os.Stat(path); err != nil {
		static, err := instanceFont(data, vars, name)
		if err != nil {
			return nil, err
		}
		if err := writeAtomic(path, static); err != nil {
			return nil, err
		}
	}
	f, err := loadFace(font.FontID{File: path}, family, weight, false)
	if err != nil {
		return nil, err
	}
	f.Bundled = true
	return f, nil
}

// Tables a static instance leaves out: variations, hinting (made for the
// default outlines), and outlines it replaces with glyf.
var instanceDrops = []string{"fvar", "gvar", "avar", "HVAR", "VVAR", "MVAR", "STAT", "cvar",
	"fpgm", "prep", "cvt ", "hdmx", "LTSH", "VDMX", "DSIG", "CFF ", "CFF2", "VORG", "glyf", "loca"}

// instanceFont writes a static TrueType font of the variable font in data at
// vars, named name.
func instanceFont(data []byte, vars []font.Variation, name string) ([]byte, error) {
	src, err := readSFNT(data)
	if err != nil {
		return nil, err
	}
	ld, err := ot.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	ft, err := font.NewFont(ld)
	if err != nil {
		return nil, err
	}
	face := font.NewFace(ft)
	face.SetVariations(vars)
	for _, tag := range []string{"head", "hhea", "maxp", "OS/2"} {
		if src.tables[tag] == nil {
			return nil, fmt.Errorf("no %s table", tag)
		}
	}
	n := int(be16(src.tables["maxp"][4:]))

	var glyf, loca, hmtx []byte
	box := [4]int{math.MaxInt16, math.MaxInt16, math.MinInt16, math.MinInt16}
	maxPoints, maxContours, maxAdvance := 0, 0, 0
	minLSB, minRSB, maxExtent := math.MaxInt16, math.MaxInt16, math.MinInt16
	for gid := range n {
		loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
		outline, _ := face.GlyphDataOutline(font.GID(gid))
		glyph, b, points, contours := encodeGlyph(outline.Segments)
		glyf = append(glyf, glyph...)
		for len(glyf)%4 != 0 {
			glyf = append(glyf, 0)
		}
		advance := int(math.Round(float64(face.HorizontalAdvance(font.GID(gid)))))
		advance = max(advance, 0)
		lsb := 0
		if points > 0 {
			lsb = b[0]
			box = [4]int{min(box[0], b[0]), min(box[1], b[1]), max(box[2], b[2]), max(box[3], b[3])}
			minLSB, minRSB = min(minLSB, b[0]), min(minRSB, advance-b[2])
			maxExtent = max(maxExtent, b[2])
		}
		hmtx = binary.BigEndian.AppendUint16(hmtx, uint16(advance))
		hmtx = binary.BigEndian.AppendUint16(hmtx, uint16(int16(lsb)))
		maxPoints, maxContours, maxAdvance = max(maxPoints, points), max(maxContours, contours), max(maxAdvance, advance)
	}
	loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
	if box[0] > box[2] {
		box = [4]int{0, 0, 0, 0}
		minLSB, minRSB, maxExtent = 0, 0, 0
	}

	out := sfnt{version: []byte{0, 1, 0, 0}, tables: maps.Clone(src.tables)}
	for _, tag := range instanceDrops {
		delete(out.tables, tag)
	}
	out.tables["glyf"], out.tables["loca"], out.tables["hmtx"] = glyf, loca, hmtx

	head := append([]byte(nil), src.tables["head"]...)
	if len(head) < 54 {
		return nil, fmt.Errorf("short head table")
	}
	for i, v := range box {
		binary.BigEndian.PutUint16(head[36+2*i:], uint16(int16(v)))
	}
	binary.BigEndian.PutUint16(head[50:], 1) // long loca offsets
	out.tables["head"] = head

	hhea := append([]byte(nil), src.tables["hhea"]...)
	if len(hhea) < 36 {
		return nil, fmt.Errorf("short hhea table")
	}
	for at, v := range map[int]int{10: maxAdvance, 12: minLSB, 14: minRSB, 16: maxExtent, 34: n} {
		binary.BigEndian.PutUint16(hhea[at:], uint16(int16(v)))
	}
	out.tables["hhea"] = hhea

	maxp := make([]byte, 32) // version 1.0, without instructions
	binary.BigEndian.PutUint32(maxp, 0x00010000)
	binary.BigEndian.PutUint16(maxp[4:], uint16(n))
	binary.BigEndian.PutUint16(maxp[6:], uint16(maxPoints))
	binary.BigEndian.PutUint16(maxp[8:], uint16(maxContours))
	binary.BigEndian.PutUint16(maxp[14:], 2) // maxZones
	out.tables["maxp"] = maxp

	os2 := append([]byte(nil), src.tables["OS/2"]...)
	for _, v := range vars {
		if v.Tag == ot.MustNewTag("wght") && len(os2) >= 6 {
			binary.BigEndian.PutUint16(os2[4:], uint16(v.Value))
		}
	}
	out.tables["OS/2"] = os2
	out.tables["name"] = nameTable(name)
	return out.bytes()
}

// encodeGlyph writes an outline as a simple TrueType glyph: quadratic
// contours (cubic curves split into quadratic ones), without instructions.
// It returns the glyph, its bounding box (xMin, yMin, xMax, yMax), and its
// numbers of points and contours.
func encodeGlyph(segments []ot.Segment) ([]byte, [4]int, int, int) {
	type point struct {
		x, y int
		on   bool
	}
	at := func(p ot.SegmentPoint) (float64, float64) { return float64(p.X), float64(p.Y) }
	pt := func(x, y float64, on bool) point { return point{int(math.Round(x)), int(math.Round(y)), on} }
	var contours [][]point
	var cx, cy float64
	for _, s := range segments {
		switch s.Op {
		case ot.SegmentOpMoveTo:
			cx, cy = at(s.Args[0])
			contours = append(contours, []point{pt(cx, cy, true)})
			continue
		}
		if len(contours) == 0 {
			contours = append(contours, []point{pt(cx, cy, true)})
		}
		c := &contours[len(contours)-1]
		switch s.Op {
		case ot.SegmentOpLineTo:
			cx, cy = at(s.Args[0])
			*c = append(*c, pt(cx, cy, true))
		case ot.SegmentOpQuadTo:
			qx, qy := at(s.Args[0])
			cx, cy = at(s.Args[1])
			*c = append(*c, pt(qx, qy, false), pt(cx, cy, true))
		case ot.SegmentOpCubeTo:
			x1, y1 := at(s.Args[0])
			x2, y2 := at(s.Args[1])
			x3, y3 := at(s.Args[2])
			// Four quadratic pieces track a cubic within a fraction of a unit.
			const pieces = 4
			bez := func(t float64) (float64, float64) {
				u := 1 - t
				return u*u*u*cx + 3*u*u*t*x1 + 3*u*t*t*x2 + t*t*t*x3, u*u*u*cy + 3*u*u*t*y1 + 3*u*t*t*y2 + t*t*t*y3
			}
			deriv := func(t float64) (float64, float64) {
				u := 1 - t
				return 3 * (u*u*(x1-cx) + 2*u*t*(x2-x1) + t*t*(x3-x2)), 3 * (u*u*(y1-cy) + 2*u*t*(y2-y1) + t*t*(y3-y2))
			}
			for k := range pieces {
				t0, t1 := float64(k)/pieces, float64(k+1)/pieces
				ax, ay := bez(t0)
				bx, by := bez(t1)
				dax, day := deriv(t0)
				dbx, dby := deriv(t1)
				h := (t1 - t0) / 2
				// The control point where the end tangents meet, approximately.
				qx := (ax + dax*h + bx - dbx*h) / 2
				qy := (ay + day*h + by - dby*h) / 2
				*c = append(*c, pt(qx, qy, false), pt(bx, by, true))
			}
			cx, cy = x3, y3
		}
	}

	var flags, xs, ys, ends []byte
	box := [4]int{math.MaxInt, math.MaxInt, math.MinInt, math.MinInt}
	points, kept := 0, 0
	lastX, lastY := 0, 0
	for _, c := range contours {
		if len(c) > 1 && c[len(c)-1] == c[0] {
			c = c[:len(c)-1] // contours close by themselves
		}
		if len(c) < 2 {
			continue
		}
		for _, p := range c {
			flag := byte(0)
			if p.on {
				flag = 1
			}
			flags = append(flags, flag)
			xs = binary.BigEndian.AppendUint16(xs, uint16(int16(p.x-lastX)))
			ys = binary.BigEndian.AppendUint16(ys, uint16(int16(p.y-lastY)))
			lastX, lastY = p.x, p.y
			box = [4]int{min(box[0], p.x), min(box[1], p.y), max(box[2], p.x), max(box[3], p.y)}
		}
		points += len(c)
		ends = binary.BigEndian.AppendUint16(ends, uint16(points-1))
		kept++
	}
	if kept == 0 {
		return nil, [4]int{}, 0, 0
	}
	out := binary.BigEndian.AppendUint16(nil, uint16(kept))
	for _, v := range box {
		out = binary.BigEndian.AppendUint16(out, uint16(int16(v)))
	}
	out = append(out, ends...)
	out = binary.BigEndian.AppendUint16(out, 0) // no instructions
	out = append(out, flags...)
	out = append(out, xs...)
	out = append(out, ys...)
	return out, box, points, kept
}
