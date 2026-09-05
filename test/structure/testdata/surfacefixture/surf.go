// Package surfacefixture is a fixture for the exported-surface budget gate:
// six exported package-scope identifiers (Widget, New, Default, Version, and the
// grouped consts Alpha + Beta), one exported method (Do — NOT package scope,
// must not count), and unexported helpers (must not count).
// TestExportedSurfaceCounts asserts the count is 6. The grouped const block
// makes the fixture distinguish per-identifier counting (which is what the gate
// does — each exported NAME is one unit) from per-declaration counting (which
// would score the block as 1). It lives under testdata/ so the real gate (which
// scans ./...) skips it.
package surfacefixture

// Widget is an exported type (1).
type Widget struct{ n int }

// New is an exported func (2).
func New() *Widget { return &Widget{} }

// Do is an exported METHOD — part of the method set, not package scope; the
// surface metric must not count it.
func (w *Widget) Do() int { return w.n }

// Default is an exported var (3).
var Default = New()

// Version is an exported const (4).
const Version = "1"

// Alpha and Beta are two exported names in one grouped const block: each counts
// (5, 6), pinning the per-identifier metric.
const (
	Alpha = "a"
	Beta  = "b"
)

func helper() int { return 0 }

var internalCount int
