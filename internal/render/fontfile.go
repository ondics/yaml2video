package render

import (
	"encoding/binary"
	"fmt"
	"sort"
	"unicode/utf16"
)

// sfnt is a font file's tables (TrueType or OpenType, not a collection).
type sfnt struct {
	version []byte // the first four bytes
	tables  map[string][]byte
}

// readSFNT splits a font file into its tables.
func readSFNT(data []byte) (sfnt, error) {
	if len(data) < 12 {
		return sfnt{}, fmt.Errorf("not a font")
	}
	switch tag := string(data[:4]); tag {
	case "\x00\x01\x00\x00", "OTTO", "true":
	default:
		return sfnt{}, fmt.Errorf("unsupported font format %q", tag)
	}
	count := int(binary.BigEndian.Uint16(data[4:]))
	if len(data) < 12+16*count {
		return sfnt{}, fmt.Errorf("truncated table directory")
	}
	f := sfnt{version: data[:4], tables: map[string][]byte{}}
	for i := range count {
		rec := data[12+16*i:]
		offset, length := binary.BigEndian.Uint32(rec[8:]), binary.BigEndian.Uint32(rec[12:])
		if uint64(offset)+uint64(length) > uint64(len(data)) {
			return sfnt{}, fmt.Errorf("table %q is out of bounds", rec[:4])
		}
		f.tables[string(rec[:4])] = data[offset : offset+length]
	}
	return f, nil
}

// bytes writes the font file, with table checksums and the head table's
// whole-file checksum adjustment.
func (f sfnt) bytes() ([]byte, error) {
	if len(f.tables["head"]) < 12 {
		return nil, fmt.Errorf("missing head table")
	}
	tags := make([]string, 0, len(f.tables))
	for tag := range f.tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	n := len(tags)
	out := make([]byte, 12+16*n)
	copy(out, f.version)
	binary.BigEndian.PutUint16(out[4:], uint16(n))
	power := 1
	for power*2 <= n {
		power *= 2
	}
	binary.BigEndian.PutUint16(out[6:], uint16(power*16))       // searchRange
	binary.BigEndian.PutUint16(out[8:], uint16(log2(power)))    // entrySelector
	binary.BigEndian.PutUint16(out[10:], uint16(n*16-power*16)) // rangeShift
	head := -1
	for i, tag := range tags {
		body := f.tables[tag]
		if tag == "head" {
			body = append([]byte(nil), body...)
			binary.BigEndian.PutUint32(body[8:], 0) // checkSumAdjustment, set below
		}
		offset := len(out)
		out = append(out, body...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		rec := out[12+16*i:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[4:], sfntChecksum(body))
		binary.BigEndian.PutUint32(rec[8:], uint32(offset))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(body)))
		if tag == "head" {
			head = offset
		}
	}
	binary.BigEndian.PutUint32(out[head+8:], 0xB1B0AFBA-sfntChecksum(out))
	return out, nil
}

func log2(n int) int {
	k := 0
	for n > 1 {
		n /= 2
		k++
	}
	return k
}

// renameFont rewrites a font's name table so the font is the family name
// (style Regular), keeping every other table as it is. libass then finds it
// by that name and nothing else.
func renameFont(data []byte, name string) ([]byte, error) {
	f, err := readSFNT(data)
	if err != nil {
		return nil, err
	}
	f.tables["name"] = nameTable(name)
	return f.bytes()
}

// sfntChecksum sums data as big-endian 32-bit words, zero-padded.
func sfntChecksum(data []byte) uint32 {
	var sum uint32
	for i := 0; i < len(data); i += 4 {
		var word [4]byte
		copy(word[:], data[i:])
		sum += binary.BigEndian.Uint32(word[:])
	}
	return sum
}

// nameTable is a name table naming the family name, style Regular, in the
// Windows (UTF-16) and Mac (ASCII) records font loaders read.
func nameTable(name string) []byte {
	postscript := make([]rune, 0, len(name))
	for _, r := range name {
		if r > ' ' && r < 127 && r != '[' && r != ']' && r != '(' && r != ')' && r != '{' && r != '}' && r != '<' && r != '>' && r != '/' && r != '%' {
			postscript = append(postscript, r)
		}
	}
	values := map[uint16]string{1: name, 2: "Regular", 4: name, 6: string(postscript[:min(len(postscript), 63)])}
	ids := []uint16{1, 2, 4, 6}

	type record struct {
		platform, encoding, language, id uint16
		text                             []byte
	}
	var records []record
	for _, id := range ids { // Mac Roman: ASCII only
		ascii := make([]byte, 0, len(values[id]))
		for _, r := range values[id] {
			if r < 128 {
				ascii = append(ascii, byte(r))
			}
		}
		records = append(records, record{1, 0, 0, id, ascii})
	}
	for _, id := range ids { // Windows Unicode BMP, en-US
		var text []byte
		for _, u := range utf16.Encode([]rune(values[id])) {
			text = binary.BigEndian.AppendUint16(text, u)
		}
		records = append(records, record{3, 1, 0x409, id, text})
	}

	header := 6 + 12*len(records)
	out := make([]byte, header)
	binary.BigEndian.PutUint16(out[2:], uint16(len(records)))
	binary.BigEndian.PutUint16(out[4:], uint16(header))
	var strings []byte
	for i, r := range records {
		rec := out[6+12*i:]
		for k, v := range []uint16{r.platform, r.encoding, r.language, r.id, uint16(len(r.text)), uint16(len(strings))} {
			binary.BigEndian.PutUint16(rec[2*k:], v)
		}
		strings = append(strings, r.text...)
	}
	return append(out, strings...)
}
