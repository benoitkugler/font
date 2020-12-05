package sfnt

import (
	"errors"
	"fmt"
)

var (
	errInvalidGPOSKern           = errors.New("invalid GPOS kerning subtable")
	errUnsupportedClassDefFormat = errors.New("unsupported class definition format")
)

type classKerns struct {
	coverage       map[GlyphIndex]struct{}
	class1, class2 map[GlyphIndex]int
	numClass2      int
	kerns          []int16 // size numClass1 * numClass2
}

func (c classKerns) KernPair(left, right GlyphIndex) (int16, bool) {
	// check coverage to avoid selection of default class 0
	_, found := c.coverage[left]
	if !found {
		return 0, false
	}
	idxa := c.class1[left]
	idxb := c.class2[right]
	return c.kerns[idxb+idxa*c.numClass2], true
}

func (c classKerns) Size() int {
	return len(c.class1) * len(c.class2)
}

func (t TableLayout) parseKern() (Kerns, error) {
	simples := simpleKerns{}

	classes := kernUnions{nil} // room for 'simples'

	for _, lookup := range t.Lookups {
		if lookup.Type == 2 {
			for _, subtableOffset := range lookup.subtableOffsets {
				b := lookup.data
				if len(b) < 4+int(subtableOffset) {
					return nil, errInvalidGPOSKern
				}
				b = b[subtableOffset:]
				format, coverageOffset := be.Uint16(b), be.Uint16(b[2:])

				coverage, err := fetchCoverage(b, int(coverageOffset))
				if err != nil {
					return nil, err
				}

				switch format {
				case 1: // Adjustments for Glyph Pairs
					err := parsePairPosFormat1(b, coverage, simples)
					if err != nil {
						return nil, err
					}
				case 2: // Class Pair Adjustment
					cl, err := parsePairPosFormat2(b, coverage)
					if err != nil {
						return nil, err
					}
					classes = append(classes, cl)
				}
			}
		}
	}
	// dont forget to add the "simple" kerns
	classes[0] = simples

	if len(classes) == 1 && len(simples) == 0 {
		// no kerning information
		return nil, errors.New("missing GPOS kerning information")
	}

	return classes, nil
}

// if l[i] = gi then gi has coverage index of i
func fetchCoverage(buf []byte, offset int) ([]GlyphIndex, error) {
	if len(buf) < offset+2 { // format and count
		return nil, errInvalidGPOSKern
	}
	buf = buf[offset:]
	switch format := be.Uint16(buf); format {
	case 1:
		// Coverage Format 1: coverageFormat, glyphCount, []glyphArray
		return fetchCoverageList(buf[2:])
	case 2:
		// Coverage Format 2: coverageFormat, rangeCount, []rangeRecords{startGlyphID, endGlyphID, startCoverageIndex}
		return fetchCoverageRange(buf[2:])
	default:
		return nil, fmt.Errorf("unsupported GPOS coverage format %d", format)
	}
}

func fetchCoverageList(buf []byte) ([]GlyphIndex, error) {
	const headerSize, entrySize = 2, 2
	if len(buf) < headerSize {
		return nil, errInvalidGPOSKern
	}

	num := int(be.Uint16(buf))
	if len(buf) < headerSize+num*entrySize {
		return nil, errInvalidGPOSKern
	}

	out := make([]GlyphIndex, num)
	for i := range out {
		out[i] = GlyphIndex(be.Uint16(buf[headerSize+2*i:]))
	}
	return out, nil
}

type coverageRange struct {
	start, end    GlyphIndex
	startCoverage uint16
}

type coverageRanges []coverageRange

func (crs coverageRanges) list() []GlyphIndex {
	out := make([]GlyphIndex, 0, len(crs))
	for _, cr := range crs {
		for i := cr.start; i <= cr.end; i++ {
			out = append(out, i)
		}
	}
	return out
}

func fetchCoverageRange(buf []byte) ([]GlyphIndex, error) {
	const headerSize, entrySize = 2, 6
	if len(buf) < headerSize {
		return nil, errInvalidGPOSKern
	}

	num := int(be.Uint16(buf))
	if len(buf) < headerSize+num*entrySize {
		return nil, errInvalidGPOSKern
	}

	out := make(coverageRanges, num)
	for i := range out {
		out[i].start = GlyphIndex(be.Uint16(buf[headerSize+i*entrySize:]))
		out[i].end = GlyphIndex(be.Uint16(buf[headerSize+i*entrySize+2:]))
		out[i].startCoverage = be.Uint16(buf[headerSize+i*entrySize+4:])
	}
	return out.list(), nil
}

// offset int
func parsePairPosFormat1(buf []byte, coverage []GlyphIndex, out simpleKerns) error {
	// PairPos Format 1: posFormat, coverageOffset, valueFormat1,
	// valueFormat2, pairSetCount, []pairSetOffsets
	const headerSize = 10 // including posFormat and coverageOffset
	if len(buf) < headerSize {
		return errInvalidGPOSKern
	}
	valueFormat1, valueFormat2, nPairs := be.Uint16(buf[4:]), be.Uint16(buf[6:]), int(be.Uint16(buf[8:]))

	// check valueFormat1 and valueFormat2 flags
	if valueFormat1 != 0x04 || valueFormat2 != 0x00 {
		// we only support kerning with X_ADVANCE for first glyph
		return nil
	}

	// PairPos table contains an array of offsets to PairSet
	// tables, which contains an array of PairValueRecords.
	// Calculate length of complete PairPos table by jumping to
	// last PairSet.
	// We need to iterate all offsets to find the last pair as
	// offsets are not sorted and can be repeated.
	if len(buf) < headerSize+nPairs*2 {
		return errInvalidGPOSKern
	}
	var lastPairSetOffset int
	for n := 0; n < nPairs; n++ {
		pairOffset := int(be.Uint16(buf[headerSize+n*2:]))
		if pairOffset > lastPairSetOffset {
			lastPairSetOffset = pairOffset
		}
	}

	if len(buf) < lastPairSetOffset+2 {
		return errInvalidGPOSKern
	}

	pairValueCount := int(be.Uint16(buf[lastPairSetOffset:]))
	// Each PairSet contains the secondGlyph (u16) and one or more value records (all u16).
	// We only support lookup tables with one value record (X_ADVANCE, see valueFormat1/2 above).
	lastPairSetLength := 2 + pairValueCount*4

	length := lastPairSetOffset + lastPairSetLength
	if len(buf) < length {
		return errInvalidGPOSKern
	}
	return fetchPairPosGlyph(coverage, nPairs, buf, out)
}

func fetchPairPosGlyph(coverage []GlyphIndex, num int, glyphs []byte, out simpleKerns) error {
	for idx, a := range coverage {
		if idx >= num {
			return errInvalidGPOSKern
		}

		offset := int(be.Uint16(glyphs[10+idx*2:]))
		if offset+1 >= len(glyphs) {
			return errInvalidGPOSKern
		}

		highByte := uint32(a) << 16
		count := int(be.Uint16(glyphs[offset:]))
		for i := 0; i < count; i++ {
			b := GlyphIndex(int(be.Uint16(glyphs[offset+2+i*4:])))
			value := int16(be.Uint16(glyphs[offset+2+i*4+2:]))
			out[highByte|uint32(b)] = value
		}
	}
	return nil
}

func parsePairPosFormat2(buf []byte, coverage []GlyphIndex) (classKerns, error) {
	// PairPos Format 2:
	// posFormat, coverageOffset, valueFormat1, valueFormat2,
	// classDef1Offset, classDef2Offset, class1Count, class2Count,
	// []class1Records
	const headerSize = 16 // including posFormat and coverageOffset
	if len(buf) < headerSize {
		return classKerns{}, errInvalidGPOSKern
	}

	valueFormat1, valueFormat2 := be.Uint16(buf[4:]), be.Uint16(buf[6:])
	// check valueFormat1 and valueFormat2 flags
	if valueFormat1 != 0x04 || valueFormat2 != 0x00 {
		// we only support kerning with X_ADVANCE for first glyph
		return classKerns{}, nil
	}

	cdef1Offset := int(be.Uint16(buf[8:]))
	cdef2Offset := int(be.Uint16(buf[10:]))
	numClass1 := int(be.Uint16(buf[12:]))
	numClass2 := int(be.Uint16(buf[14:]))
	// var cdef1, cdef2 classLookupFunc
	cdef1, err := fetchClassLookup(buf, cdef1Offset)
	if err != nil {
		return classKerns{}, err
	}
	cdef2, err := fetchClassLookup(buf, cdef2Offset)
	if err != nil {
		return classKerns{}, err
	}

	return fetchPairPosClass(
		buf[headerSize:],
		coverage,
		numClass1,
		numClass2,
		cdef1,
		cdef2,
	)
}

func fetchClassLookup(buf []byte, offset int) (class, error) {
	if len(buf) < offset+2 {
		return nil, errInvalidGPOSKern
	}
	buf = buf[offset:]
	switch be.Uint16(buf) {
	case 1:
		return fetchClassLookupFormat1(buf)
	case 2:
		// ClassDefFormat 2: classFormat, classRangeCount, []classRangeRecords
		return fetchClassLookupFormat2(buf)
	default:
		return nil, errUnsupportedClassDefFormat
	}
}

type class interface {
	classIDs() map[GlyphIndex]int
}

type class1 struct {
	startGlyph     GlyphIndex
	targetClassIDs []int // array of target class IDs. gi is the index into that array (minus startGI).
}

func (c class1) classIDs() map[GlyphIndex]int {
	out := make(map[GlyphIndex]int, len(c.targetClassIDs))
	for i, target := range c.targetClassIDs {
		out[c.startGlyph+GlyphIndex(i)] = target
	}
	return out
}

// ClassDefFormat 1: classFormat, startGlyphID, glyphCount, []classValueArray
func fetchClassLookupFormat1(buf []byte) (class1, error) {
	const headerSize = 6 // including classFormat
	if len(buf) < headerSize {
		return class1{}, errInvalidGPOSKern
	}

	startGI := GlyphIndex(be.Uint16(buf[2:]))
	num := int(be.Uint16(buf[4:]))
	if len(buf) < headerSize+num*2 {
		return class1{}, errInvalidGPOSKern
	}

	classIDs := make([]int, num)
	for i := range classIDs {
		classIDs[i] = int(be.Uint16(buf[6+i*2:]))
	}
	return class1{startGlyph: startGI, targetClassIDs: classIDs}, nil
}

type classRangeRecord struct {
	start, end    GlyphIndex
	targetClassID int
}

type class2 []classRangeRecord

func (c class2) classIDs() map[GlyphIndex]int {
	out := make(map[GlyphIndex]int, len(c))
	for _, cr := range c {
		for i := cr.start; i <= cr.end; i++ {
			out[i] = cr.targetClassID
		}
	}
	return out
}

// ClassDefFormat 2: classFormat, classRangeCount, []classRangeRecords
func fetchClassLookupFormat2(buf []byte) (class2, error) {
	const headerSize = 4 // including classFormat
	if len(buf) < headerSize {
		return nil, errInvalidGPOSKern
	}

	num := int(be.Uint16(buf[2:]))
	if len(buf) < headerSize+num*6 {
		return nil, errInvalidGPOSKern
	}

	out := make(class2, num)
	for i := range out {
		out[i].start = GlyphIndex(be.Uint16(buf[headerSize+i*6:]))
		out[i].end = GlyphIndex(be.Uint16(buf[headerSize+i*6+2:]))
		out[i].targetClassID = int(be.Uint16(buf[headerSize+i*6+4:]))
	}
	return out, nil
}

func fetchPairPosClass(buf []byte, cov []GlyphIndex, num1, num2 int, cdef1, cdef2 class) (classKerns, error) {
	if len(buf) < num1*num2*2 {
		return classKerns{}, errInvalidGPOSKern
	}

	kerns := make([]int16, num1*num2)
	for i := 0; i < num1; i++ {
		for j := 0; j < num2; j++ {
			index := j + i*num2
			kerns[index] = int16(be.Uint16(buf[index*2:]))
		}
	}

	coverage := make(map[GlyphIndex]struct{}, len(cov))
	for _, c := range cov {
		coverage[c] = struct{}{}
	}

	return classKerns{
		coverage:  coverage,
		class1:    cdef1.classIDs(),
		class2:    cdef2.classIDs(),
		kerns:     kerns,
		numClass2: num2,
	}, nil
}
