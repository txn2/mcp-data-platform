package pmtiles

import (
	"errors"
	"math"
	"sort"
)

// MaxZoom is the deepest zoom an extract may ask for. Tile ids are uint64 and
// the specification allows deeper, but no basemap is built past 15 and the
// cover's work grows with the zoom.
const MaxZoom = 15

// The edges of the world in degrees, and where web mercator's square ends.
const (
	maxLon         = 180
	maxLat         = 90
	fullTurn       = 360
	maxMercatorLat = 85.0511287798066
)

// Bounds is a bounding box in degrees.
type Bounds struct {
	MinLon float64 `json:"min_lon"`
	MinLat float64 `json:"min_lat"`
	MaxLon float64 `json:"max_lon"`
	MaxLat float64 `json:"max_lat"`
}

// The refusals Validate makes.
var (
	errLonRange = errors.New("longitude must be between -180 and 180")
	errLatRange = errors.New("latitude must be between -90 and 90")
	errLonOrder = errors.New("min_lon must be less than max_lon")
	errLatOrder = errors.New("min_lat must be less than max_lat")
)

// Validate refuses a box that is not one: out of range, or with a minimum
// that is not below its maximum. A box crossing the antimeridian is two boxes.
func (b Bounds) Validate() error {
	switch {
	case b.MinLon < -maxLon || b.MaxLon > maxLon:
		return errLonRange
	case b.MinLat < -maxLat || b.MaxLat > maxLat:
		return errLatRange
	case b.MinLon >= b.MaxLon:
		return errLonOrder
	case b.MinLat >= b.MaxLat:
		return errLatOrder
	}
	return nil
}

// Intersect returns the overlap of two boxes and whether there is one.
func (b Bounds) Intersect(o Bounds) (Bounds, bool) {
	r := Bounds{
		MinLon: math.Max(b.MinLon, o.MinLon), MinLat: math.Max(b.MinLat, o.MinLat),
		MaxLon: math.Min(b.MaxLon, o.MaxLon), MaxLat: math.Min(b.MaxLat, o.MaxLat),
	}
	return r, r.MinLon < r.MaxLon && r.MinLat < r.MaxLat
}

// Range is a half-open run of tile ids, [Start, End).
type Range struct {
	Start uint64
	End   uint64
}

// zoomBase is the first tile id at zoom z: the count of every tile at a
// shallower zoom, (4^z - 1) / 3.
func zoomBase(z uint8) uint64 {
	const tilesPerLevel = 3
	return (uint64(1)<<(2*uint64(z)) - 1) / tilesPerLevel
}

// tileRect is the tiles at one zoom a box touches, inclusive.
type tileRect struct {
	x0, y0, x1, y1 uint32
}

// rectAt returns the tiles at zoom z a box touches.
func rectAt(b Bounds, z uint8) tileRect {
	n := float64(uint64(1) << z)
	maxIdx := n - 1
	tx := func(lon float64) uint32 {
		return uint32(math.Min(maxIdx, math.Max(0, math.Floor((lon+maxLon)/fullTurn*n))))
	}
	ty := func(lat float64) uint32 {
		lat = math.Max(-maxMercatorLat, math.Min(maxMercatorLat, lat)) * math.Pi / maxLon
		y := (1 - math.Log(math.Tan(lat)+1/math.Cos(lat))/math.Pi) / 2 * n
		return uint32(math.Min(maxIdx, math.Max(0, math.Floor(y))))
	}
	return tileRect{x0: tx(b.MinLon), y0: ty(b.MaxLat), x1: tx(b.MaxLon), y1: ty(b.MinLat)}
}

// cell is one quadtree cell: a tile at a shallower level than the zoom being
// covered, standing for every tile under it.
type cell struct {
	level  uint8
	cx, cy uint32
}

// quarters is how many cells one level down a cell divides into.
const quarters = 4

// children are a cell's quarters, one level down.
func (c cell) children() [quarters]cell {
	l := c.level + 1
	x, y := c.cx*2, c.cy*2
	return [quarters]cell{{l, x, y}, {l, x + 1, y}, {l, x, y + 1}, {l, x + 1, y + 1}}
}

// Cover returns the tile ids a box touches at every zoom from minZoom to
// maxZoom, as sorted, merged ranges.
//
// A Hilbert curve visits each quadtree cell's tiles consecutively, so a cell
// entirely inside the box is one range, and the cover recurses only along
// the box's edge: the number of ranges grows with the box's perimeter in
// tiles, not its area.
func Cover(b Bounds, minZoom, maxZoom uint8) []Range {
	var out []Range
	for z := minZoom; z <= maxZoom; z++ {
		out = coverCell(out, z, cell{}, rectAt(b, z))
	}
	return mergeRanges(out)
}

func coverCell(out []Range, z uint8, c cell, r tileRect) []Range {
	shift := z - c.level
	lo := func(v uint32) uint32 { return v << shift }
	hi := func(v uint32) uint32 { return (v+1)<<shift - 1 }
	if hi(c.cx) < r.x0 || lo(c.cx) > r.x1 || hi(c.cy) < r.y0 || lo(c.cy) > r.y1 {
		return out
	}
	if lo(c.cx) >= r.x0 && hi(c.cx) <= r.x1 && lo(c.cy) >= r.y0 && hi(c.cy) <= r.y1 {
		h := ZxyToID(c.level, c.cx, c.cy) - zoomBase(c.level)
		size := uint64(1) << (2 * uint64(shift))
		start := zoomBase(z) + h*size
		return append(out, Range{Start: start, End: start + size})
	}
	for _, child := range c.children() {
		out = coverCell(out, z, child, r)
	}
	return out
}

func mergeRanges(rs []Range) []Range {
	if len(rs) == 0 {
		return rs
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
	out := rs[:1]
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			last.End = max(last.End, r.End)
			continue
		}
		out = append(out, r)
	}
	return out
}

// clip returns the parts of [start, end) inside the sorted ranges.
func clip(rs []Range, start, end uint64) []Range {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].End > start })
	var out []Range
	for ; i < len(rs) && rs[i].Start < end; i++ {
		out = append(out, Range{Start: max(start, rs[i].Start), End: min(end, rs[i].End)})
	}
	return out
}

// rotate is the Hilbert curve's quadrant rotation.
func rotate(n, x, y, rx, ry uint32) (nx, ny uint32) {
	if ry == 0 {
		if rx != 0 {
			x = n - 1 - x
			y = n - 1 - y
		}
		return y, x
	}
	return x, y
}

// ZxyToID returns the tile id of tile (z, x, y): the tiles of every shallower
// zoom, then the tile's position along the zoom's Hilbert curve.
func ZxyToID(z uint8, x, y uint32) uint64 {
	const quadrantBits = 3
	acc := zoomBase(z)
	for s := uint32(1) << z >> 1; s > 0; s >>= 1 {
		rx := s & x
		ry := s & y
		acc += uint64(s) * uint64((quadrantBits*rx)^ry)
		x, y = rotate(s, x, y, rx, ry)
	}
	return acc
}
