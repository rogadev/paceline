package main

import (
	"bytes"
	"compress/zlib"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// fontFile is Cascadia Mono Regular, subset to the characters the demo draws
// and saved as WOFF. docs/demo/README.md records the subsetting command;
// font/OFL.txt is its licence.
//
//go:embed font/CascadiaMono-Regular-subset.woff
var fontFile []byte

// fontFace is what the SVG writer needs from a font: the size of its em and
// how far each character it covers advances the pen, both in font units.
type fontFace struct {
	unitsPerEm int
	advances   map[rune]int
}

// embeddedFont parses the font the SVG embeds.
func embeddedFont() (*fontFace, error) {
	face, err := parseWOFF(fontFile)
	if err != nil {
		return nil, fmt.Errorf("the embedded font: %w", err)
	}
	return face, nil
}

// parseWOFF reads a WOFF 1.0 font's em size, character map, and advance
// widths. It reads only the head, hhea, hmtx, and cmap tables, and only a
// Unicode cmap in format 4, which is what the subsetter writes for a font
// whose characters are all in the Basic Multilingual Plane.
func parseWOFF(data []byte) (*fontFace, error) {
	tables, err := woffTables(data)
	if err != nil {
		return nil, err
	}
	head, hhea, hmtx, cmap := tables["head"], tables["hhea"], tables["hmtx"], tables["cmap"]
	if len(head) < 54 {
		return nil, errors.New("the head table is missing or short")
	}
	if len(hhea) < 36 {
		return nil, errors.New("the hhea table is missing or short")
	}
	face := &fontFace{unitsPerEm: int(binary.BigEndian.Uint16(head[18:]))}
	if face.unitsPerEm == 0 {
		return nil, errors.New("the head table has no units per em")
	}
	numHMetrics := int(binary.BigEndian.Uint16(hhea[34:]))
	if numHMetrics == 0 || len(hmtx) < 4*numHMetrics {
		return nil, fmt.Errorf("the hmtx table is shorter than its %d metrics", numHMetrics)
	}
	glyphs, err := cmapFormat4(cmap)
	if err != nil {
		return nil, err
	}
	face.advances = make(map[rune]int, len(glyphs))
	for r, gid := range glyphs {
		// Glyphs past the last metric share its advance.
		i := min(gid, numHMetrics-1)
		face.advances[r] = int(binary.BigEndian.Uint16(hmtx[4*i:]))
	}
	return face, nil
}

// woffTables returns a WOFF font's tables by tag, decompressed.
func woffTables(data []byte) (map[string][]byte, error) {
	const headerSize, entrySize = 44, 20
	if len(data) < headerSize || string(data[:4]) != "wOFF" {
		return nil, errors.New("not a WOFF file")
	}
	numTables := int(binary.BigEndian.Uint16(data[12:]))
	if len(data) < headerSize+numTables*entrySize {
		return nil, errors.New("the table directory is cut short")
	}
	tables := make(map[string][]byte, numTables)
	for i := range numTables {
		entry := data[headerSize+i*entrySize:]
		tag := string(entry[:4])
		offset := int(binary.BigEndian.Uint32(entry[4:]))
		compLength := int(binary.BigEndian.Uint32(entry[8:]))
		origLength := int(binary.BigEndian.Uint32(entry[12:]))
		if offset < 0 || compLength < 0 || offset+compLength > len(data) || compLength > origLength {
			return nil, fmt.Errorf("table %q lies outside the file", tag)
		}
		raw := data[offset : offset+compLength]
		if compLength == origLength {
			tables[tag] = raw
			continue
		}
		table, err := inflate(raw, origLength)
		if err != nil {
			return nil, fmt.Errorf("table %q: %w", tag, err)
		}
		tables[tag] = table
	}
	return tables, nil
}

// inflate decompresses one zlib-compressed WOFF table of a known size.
func inflate(raw []byte, size int) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	out := make([]byte, size)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, err
	}
	return out, zr.Close()
}

// cmapFormat4 maps each character in the font's Windows Unicode (3,1) or
// Unicode BMP (0,3) character map to its glyph index.
func cmapFormat4(cmap []byte) (map[rune]int, error) {
	if len(cmap) < 4 {
		return nil, errors.New("the cmap table is missing or short")
	}
	numRecords := int(binary.BigEndian.Uint16(cmap[2:]))
	if len(cmap) < 4+8*numRecords {
		return nil, errors.New("the cmap table's records are cut short")
	}
	for i := range numRecords {
		rec := cmap[4+8*i:]
		platform, encoding := binary.BigEndian.Uint16(rec), binary.BigEndian.Uint16(rec[2:])
		offset := int(binary.BigEndian.Uint32(rec[4:]))
		unicodeBMP := platform == 3 && encoding == 1 || platform == 0 && encoding == 3
		if !unicodeBMP || offset+2 > len(cmap) || binary.BigEndian.Uint16(cmap[offset:]) != 4 {
			continue
		}
		return readFormat4(cmap[offset:])
	}
	return nil, errors.New("the cmap table has no Unicode subtable in format 4")
}

// readFormat4 decodes a cmap format 4 subtable: parallel arrays of segment
// ends, starts, deltas, and range offsets, then the glyph index array the
// range offsets point into.
func readFormat4(sub []byte) (map[rune]int, error) {
	if len(sub) < 14 {
		return nil, errors.New("the cmap format 4 subtable is short")
	}
	segCount := int(binary.BigEndian.Uint16(sub[6:])) / 2
	ends := 14
	starts := ends + 2*segCount + 2
	deltas := starts + 2*segCount
	rangeOffsets := deltas + 2*segCount
	if len(sub) < rangeOffsets+2*segCount {
		return nil, errors.New("the cmap format 4 segments are cut short")
	}
	u16 := func(off int) int { return int(binary.BigEndian.Uint16(sub[off:])) }
	glyphs := make(map[rune]int)
	for s := range segCount {
		start, end := u16(starts+2*s), u16(ends+2*s)
		delta, rangeOffset := u16(deltas+2*s), u16(rangeOffsets+2*s)
		for c := start; c <= end && c != 0xffff; c++ {
			gid := c
			if rangeOffset != 0 {
				at := rangeOffsets + 2*s + rangeOffset + 2*(c-start)
				if at+2 > len(sub) {
					return nil, fmt.Errorf("the cmap entry for %U points outside the table", c)
				}
				if gid = u16(at); gid == 0 {
					continue
				}
			}
			if gid = (gid + delta) & 0xffff; gid != 0 {
				glyphs[rune(c)] = gid
			}
		}
	}
	return glyphs, nil
}
