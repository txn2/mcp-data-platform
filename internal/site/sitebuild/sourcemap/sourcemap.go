// Package sourcemap reads a version 3 source map and turns a stack trace from
// the minified code it maps into the original files, lines and columns
// (#2784, manage_site symbolicate).
package sourcemap

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Map is a parsed source map.
type Map struct {
	sources []string
	names   []string
	// lines holds, for each generated line, its segments in column order.
	lines [][]segment
}

type segment struct {
	genCol, src, srcLine, srcCol, name int
	hasSrc, hasName                    bool
}

// maxMapBytes bounds the map read.
const maxMapBytes = 64 << 20

// Parse reads a source map.
func Parse(data []byte) (*Map, error) {
	if len(data) > maxMapBytes {
		return nil, fmt.Errorf("the source map is %d bytes, more than the %d read", len(data), maxMapBytes)
	}
	var doc struct {
		Version    int      `json:"version"`
		Sources    []string `json:"sources"`
		SourceRoot string   `json:"sourceRoot"`
		Names      []string `json:"names"`
		Mappings   string   `json:"mappings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not a source map: %w", err)
	}
	if doc.Version != mapVersion {
		return nil, fmt.Errorf("source map version %d; version %d is read", doc.Version, mapVersion)
	}
	m := &Map{names: doc.Names}
	for _, s := range doc.Sources {
		m.sources = append(m.sources, cleanSource(doc.SourceRoot, s))
	}
	lines, err := decode(doc.Mappings)
	if err != nil {
		return nil, err
	}
	m.lines = lines
	return m, nil
}

// cleanSource is a source as a person names it: the compile's namespaces and
// the climb to the root removed.
func cleanSource(root, s string) string {
	s = path.Join(root, s)
	for _, ns := range []string{"site:", "url:", "cssurl:"} {
		s = strings.TrimPrefix(s, ns)
	}
	for strings.HasPrefix(s, "../") {
		s = strings.TrimPrefix(s, "../")
	}
	return strings.TrimPrefix(s, "/")
}

const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

var errMappings = errors.New("the source map's mappings cannot be read")

// decode reads the mappings: lines split by ;, segments by ,, each a run of
// base64 VLQ fields relative to the one before.
func decode(mappings string) ([][]segment, error) {
	var lines [][]segment
	var at position
	for line := range strings.SplitSeq(mappings, ";") {
		segs, err := at.line(line)
		if err != nil {
			return nil, err
		}
		slices.SortStableFunc(segs, func(a, b segment) int { return a.genCol - b.genCol })
		lines = append(lines, segs)
	}
	return lines, nil
}

// The fields a segment carries: its column, then the source, line and
// column, then the name; each relative to the one before.
const (
	mapVersion      = 3
	fieldsWithSrc   = 4
	fieldsWithName  = 5
	vlqContinuation = 32
	vlqDigit        = vlqContinuation - 1
	vlqShift        = 5
	vlqMaxShift     = 30
	urlSchemeSep    = "://"
)

// position is where the last segment left the source, line, column and name,
// which a mapping carries across lines.
type position struct{ src, srcLine, srcCol, name int }

// line reads one generated line's segments.
func (at *position) line(line string) ([]segment, error) {
	var segs []segment
	genCol := 0
	for field := range strings.SplitSeq(line, ",") {
		if field == "" {
			continue
		}
		vals, err := vlq(field)
		if err != nil {
			return nil, err
		}
		genCol += vals[0]
		s := segment{genCol: genCol}
		if len(vals) >= fieldsWithSrc {
			at.src, at.srcLine, at.srcCol = at.src+vals[1], at.srcLine+vals[2], at.srcCol+vals[fieldsWithSrc-1]
			s.src, s.srcLine, s.srcCol, s.hasSrc = at.src, at.srcLine, at.srcCol, true
		}
		if len(vals) >= fieldsWithName {
			at.name += vals[fieldsWithSrc]
			s.name, s.hasName = at.name, true
		}
		segs = append(segs, s)
	}
	return segs, nil
}

func vlq(field string) ([]int, error) {
	var out []int
	value, shift := 0, 0
	for i := 0; i < len(field); i++ {
		d := strings.IndexByte(base64Chars, field[i])
		if d < 0 {
			return nil, errMappings
		}
		value += (d & vlqDigit) << shift
		if d&vlqContinuation != 0 {
			shift += vlqShift
			if shift > vlqMaxShift {
				return nil, errMappings
			}
			continue
		}
		n := value >> 1
		if value&1 != 0 {
			n = -n
		}
		out = append(out, n)
		value, shift = 0, 0
	}
	if shift != 0 || len(out) == 0 {
		return nil, errMappings
	}
	return out, nil
}

// Position is a place in an original source.
type Position struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Name   string `json:"name,omitempty"`
}

// Lookup is the original place of a 1-based line and column in the generated
// code, false when the map says nothing about it.
func (m *Map) Lookup(line, column int) (Position, bool) {
	if line < 1 || line > len(m.lines) {
		return Position{}, false
	}
	segs := m.lines[line-1]
	i := segmentAt(segs, column-1)
	if i < 0 && len(segs) > 0 {
		// A column before the first mapped one is read as that one: a
		// trace's column can point at the indentation before a statement.
		i = 0
	}
	if i < 0 || !segs[i].hasSrc || segs[i].src >= len(m.sources) {
		return Position{}, false
	}
	s := segs[i]
	p := Position{Source: m.sources[s.src], Line: s.srcLine + 1, Column: s.srcCol + 1}
	if s.hasName && s.name < len(m.names) {
		p.Name = m.names[s.name]
	}
	return p, true
}

// segmentAt is the last segment at or before col, -1 for none.
func segmentAt(segs []segment, col int) int {
	i, found := slices.BinarySearchFunc(segs, col, func(s segment, c int) int { return s.genCol - c })
	if found {
		return i
	}
	return i - 1
}

// Frame is one location in a stack trace and where it maps to.
type Frame struct {
	// Text is the location as written in the trace.
	Text   string    `json:"text"`
	File   string    `json:"file"`
	Line   int       `json:"line"`
	Column int       `json:"column"`
	Mapped *Position `json:"mapped,omitempty"`
	// Note says why a frame was not mapped.
	Note string `json:"note,omitempty"`
}

// location is a file:line:column in a trace: Chrome's "at f (url:1:2)",
// Firefox's "f@url:1:2", or a bare path.
var location = regexp.MustCompile(`((?:https?://[^\s()]+?)|(?:/[^\s():]+)|(?:[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)*\.m?js)):(\d+):(\d+)`)

// MaxFrames bounds the locations read from one trace.
const MaxFrames = 200

// Locations are the file locations a trace names, the file as a path of the
// site.
func Locations(trace string) []Frame {
	matches := location.FindAllStringSubmatch(trace, MaxFrames)
	out := make([]Frame, 0, len(matches))
	for _, m := range matches {
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		file := m[1]
		if i := strings.Index(file, urlSchemeSep); i >= 0 {
			rest := file[i+len(urlSchemeSep):]
			if j := strings.IndexByte(rest, '/'); j >= 0 {
				file = rest[j:]
			} else {
				file = "/"
			}
		}
		file, _, _ = strings.Cut(file, "?")
		file, _, _ = strings.Cut(file, "#")
		out = append(out, Frame{Text: m[0], File: strings.TrimPrefix(file, "/"), Line: line, Column: col})
	}
	return out
}
