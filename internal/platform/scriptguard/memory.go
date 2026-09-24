package scriptguard

import (
	"fmt"
	"maps"
	"math"
	"runtime/metrics"
	"slices"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// What one Starlark value costs on the Go heap, by kind. They are estimates,
// calibrated against runtime heap measurements of the shapes scripts hold
// (lists of row dicts, lists of strings; see TestSizeTracksTheHeap), and they
// only have to be right to within the margin a budget leaves: the point is to
// fail a run holding four hundred megabytes before the kernel kills the
// replica, not to account for every byte. They match the live heap of values
// a script builds; a page of rows decoded from a tool result measured 1.65
// times its live heap, which errs toward stopping a run early, not late.
const (
	// slotBytes is one interface value in a list or a tuple.
	slotBytes = 16
	// boxedBytes is what storing a string or a float in an interface
	// allocates; minDataBytes is the smallest allocation its data takes and
	// wordBytes the rounding above that.
	boxedBytes   = 16
	minDataBytes = 16
	wordBytes    = 8
	// listBytes and tupleBytes are a container's own header.
	listBytes  = 48
	tupleBytes = 24
	// A dict or set is its header and hash-table buckets: measured at about
	// dictBytes plus dictEntryBytes an entry, and never less than
	// minDictBytes, since its first bucket is allocated whole.
	dictBytes      = 384
	dictEntryBytes = 128
	minDictBytes   = 512
	// structFieldBytes is one named field of a struct.
	structFieldBytes = 40
)

// dataBytes is what n bytes of string data take on the heap.
func dataBytes(n int) int64 {
	return max(minDataBytes, int64((n+wordBytes-1)/wordBytes*wordBytes))
}

// tableBytes is what a dict or set of n entries takes, keys and values apart.
func tableBytes(n int) int64 {
	return max(minDictBytes, dictBytes+int64(n)*dictEntryBytes)
}

// Size estimates the heap v holds, counting a container reached twice once.
func Size(v starlark.Value) int64 {
	w := walker{seen: map[any]bool{}}
	w.value(v)
	return w.total
}

// walker sums the values it is shown, remembering the containers it has
// counted so a value shared by two names is not counted twice.
type walker struct {
	seen  map[any]bool
	total int64
}

// first reports whether a container is being reached for the first time.
func (w *walker) first(c any) bool {
	if w.seen[c] {
		return false
	}
	w.seen[c] = true
	return true
}

// value adds v and everything it holds.
func (w *walker) value(v starlark.Value) {
	if size, scalar := scalarSize(v); scalar {
		w.total += size
		return
	}
	switch x := v.(type) {
	case *starlark.List:
		w.list(x)
	case starlark.Tuple:
		w.total += tupleBytes + int64(len(x))*slotBytes
		for _, e := range x {
			w.value(e)
		}
	case *starlark.Dict:
		w.dict(x)
	case *starlark.Set:
		w.set(x)
	case *starlarkstruct.Struct:
		w.structure(x)
	}
	// Functions, builtins and modules are code and bindings the platform
	// predeclared, not data the script built.
}

// scalarSize is what a value that holds no other value costs, and whether v
// is one.
func scalarSize(v starlark.Value) (int64, bool) {
	switch x := v.(type) {
	case nil, starlark.NoneType, starlark.Bool:
		return 0, true
	case starlark.String:
		return boxedBytes + dataBytes(len(x)), true
	case starlark.Bytes:
		return boxedBytes + dataBytes(len(x)), true
	case starlark.Float:
		return boxedBytes, true
	case starlark.Int:
		// A small int is held in the interface itself; a big one carries
		// its digits.
		if _, small := x.Int64(); small {
			return 0, true
		}
		return boxedBytes + int64(x.BigInt().BitLen()/wordBytes), true
	default:
		return 0, false
	}
}

func (w *walker) list(l *starlark.List) {
	if !w.first(l) {
		return
	}
	w.total += listBytes + int64(l.Len())*slotBytes
	for i := range l.Len() {
		w.value(l.Index(i))
	}
}

func (w *walker) dict(d *starlark.Dict) {
	if !w.first(d) {
		return
	}
	w.total += tableBytes(d.Len())
	// Iterate and Get rather than Items: Items allocates a tuple per entry,
	// which for a list of row dicts is a copy of the data being measured.
	it := d.Iterate()
	defer it.Done()
	var k starlark.Value
	for it.Next(&k) {
		w.value(k)
		if v, found, err := d.Get(k); err == nil && found {
			w.value(v)
		}
	}
}

func (w *walker) set(s *starlark.Set) {
	if !w.first(s) {
		return
	}
	w.total += tableBytes(s.Len())
	it := s.Iterate()
	defer it.Done()
	var k starlark.Value
	for it.Next(&k) {
		w.value(k)
	}
}

func (w *walker) structure(s *starlarkstruct.Struct) {
	if !w.first(s) {
		return
	}
	names := s.AttrNames()
	w.total += listBytes + int64(len(names))*structFieldBytes
	for _, name := range names {
		if v, err := s.Attr(name); err == nil {
			w.value(v)
		}
	}
}

// held sums what the script can still reach from the thread: the locals of
// every active frame and the globals of the module being executed. It may be
// called only while the thread is stopped in a host call, which is the only
// time a frame's locals are stable.
//
// A value captured by a nested function lives in a cell the interpreter does
// not expose, and is not counted; it is still counted in the frame that
// created it while that frame is active.
func (w *walker) held(thread *starlark.Thread) {
	globalsCounted := false
	for depth := range thread.CallStackDepth() {
		fr := thread.DebugFrame(depth)
		fn, ok := fr.Callable().(*starlark.Function)
		if !ok {
			continue
		}
		for i := range fr.NumLocals() {
			_, v := fr.Local(i)
			w.value(v)
		}
		if !globalsCounted {
			for _, v := range fn.Globals() {
				w.value(v)
			}
			globalsCounted = true
		}
	}
}

// walkEvery is how often the meter walks everything a run holds when nothing
// suggests it could be over its budget. Between walks it adds each result a
// host call hands the script to the last walk's total, which can only
// overstate what the run holds; a total over the budget is confirmed by a
// fresh walk before the run is failed. Walking a large heap at every call of a
// tight loop would cost more than the calls.
//
// What the script builds itself between host calls is not in that running
// total, so the clock alone let a run that grew faster than one walk a second
// finish far over its budget (#1867). The meter also reads how much the
// process has allocated since the last walk: the script cannot have come to
// hold more than was allocated, so while the last walk plus that growth fits
// the budget the run is inside it, and once it does not the meter walks.
const walkEvery = time.Second

// allocatedMetric is the process's cumulative heap allocation, which only
// grows: every byte a run holds was allocated after the walk that last
// measured it or was counted by that walk.
const allocatedMetric = "/gc/heap/allocs:bytes"

// allocated reads allocatedMetric. It reads a counter the runtime already
// keeps and stops nothing, so it is cheap enough for every host call.
func allocated() int64 {
	sample := []metrics.Sample{{Name: allocatedMetric}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return int64(min(sample[0].Value.Uint64(), math.MaxInt64)) // #nosec G115 -- clamped to the int64 range
}

// Meter measures what one run holds against its budget and keeps the peak.
// It is used from the interpreter's goroutine only, at host calls.
type Meter struct {
	budget int64
	// peak is the most a confirmed measure found: a walk, what the run holds
	// outside the interpreter, or what the script ended holding. A running
	// estimate is an upper bound and is never reported as the peak.
	peak int64
	// estimate is the last walk plus the results handed since and the memory
	// held outside the interpreter.
	estimate int64
	// confirmed is the last walk plus the memory held outside the
	// interpreter, with no estimate in it.
	confirmed int64
	// fresh is set while the estimate is a walk with nothing added since.
	fresh bool
	// held is memory the run holds outside the interpreter -- the pages of
	// an appended output -- which no walk of its values reaches.
	held   int64
	walked time.Time
	// allocatedAtWalk is allocated() when the last walk was taken.
	allocatedAtWalk int64
	now             func() time.Time
	allocated       func() int64
	// results counts the tool results the run has been handed, by tool, for
	// the refusal to say where the memory came from.
	results map[string]int
}

// NewMeter returns a meter for a run allowed budget bytes; zero or less sets
// no budget and still measures the peak.
func NewMeter(budget int64) *Meter {
	return &Meter{budget: budget, now: time.Now, allocated: allocated, results: map[string]int{}}
}

// Peak is the most the run was measured holding.
func (m *Meter) Peak() int64 {
	if m == nil {
		return 0
	}
	return m.peak
}

// Check measures what the thread holds at the host call named at, and refuses
// once it is over the budget. It walks the thread when the last walk is older
// than walkEvery, or when the last estimate plus what the process has
// allocated since could be over the budget. A nil meter measures nothing.
func (m *Meter) Check(thread *starlark.Thread, at string) error {
	if m == nil || thread == nil {
		return nil
	}
	if m.walked.IsZero() || m.now().Sub(m.walked) >= walkEvery || !m.fits(m.estimate+m.grown()) {
		m.walk(thread)
	}
	if m.within() {
		return nil
	}
	if !m.fresh {
		// Over on the running estimate, which only ever overstates: confirm.
		m.walk(thread)
		if m.within() {
			return nil
		}
	}
	return m.refusal(at)
}

// Called counts one tool result the run was handed, for the refusal to say
// where its memory came from.
func (m *Meter) Called(tool string) {
	if m != nil {
		m.results[tool]++
	}
}

// Handed adds a value a host call is about to hand the script to the estimate,
// and refuses when it takes the run over its budget.
func (m *Meter) Handed(thread *starlark.Thread, at string, result starlark.Value) error {
	if m == nil {
		return nil
	}
	size := Size(result)
	m.add(size)
	// What the script built since the last walk is not in the estimate; what
	// the process allocated since bounds it.
	if m.fits(m.estimate + m.grown()) {
		return nil
	}
	// Possibly over: confirm with a walk, which the result is not yet part
	// of, before failing the run.
	m.walk(thread)
	m.add(size)
	if m.within() {
		return nil
	}
	return m.refusal(at)
}

// settleAt is where a run found over its budget when it ended is said to
// have been measured.
const settleAt = "the code after its last host call"

// Settle measures what the module's globals hold when the script ends, so the
// peak includes what it built after its last host call, and refuses when that
// is over the budget: a run that ended holding more than it is allowed failed
// its budget as surely as one stopped at a host call.
func (m *Meter) Settle(globals starlark.StringDict) error {
	if m == nil {
		return nil
	}
	w := walker{seen: map[any]bool{}}
	for _, v := range globals {
		w.value(v)
	}
	m.estimate = w.total + m.held
	m.peak = max(m.peak, m.estimate)
	if m.within() {
		return nil
	}
	return m.refusal(settleAt)
}

// walk replaces the estimate with a fresh measure of what the thread holds.
func (m *Meter) walk(thread *starlark.Thread) {
	w := walker{seen: map[any]bool{}}
	if thread != nil {
		w.held(thread)
	}
	m.confirmed = w.total + m.held
	m.estimate, m.fresh, m.walked, m.allocatedAtWalk = m.confirmed, true, m.now(), m.allocated()
	m.peak = max(m.peak, m.confirmed)
}

// grown is what the process has allocated since the last walk: an upper bound
// on what the run can have added to what it holds since.
func (m *Meter) grown() int64 {
	return max(0, m.allocated()-m.allocatedAtWalk)
}

// Holding records how much the run holds outside the interpreter, replacing
// the last figure. The figure is measured, not estimated, so it counts toward
// the peak.
func (m *Meter) Holding(n int64) {
	if m == nil {
		return
	}
	m.estimate += n - m.held
	m.confirmed += n - m.held
	m.held = n
	m.peak = max(m.peak, m.confirmed)
}

// add grows the estimate by a value the run is about to hold.
func (m *Meter) add(size int64) {
	m.estimate += size
	m.fresh = false
}

// within reports whether the estimate is inside the budget, or there is none.
func (m *Meter) within() bool { return m.fits(m.estimate) }

// fits reports whether n bytes are inside the budget, or there is none.
func (m *Meter) fits(n int64) bool { return m.budget <= 0 || n <= m.budget }

// refusal is the error a run over its budget fails with.
func (m *Meter) refusal(at string) error {
	return &budgetError{held: m.estimate, budget: m.budget, at: at, results: m.resultSummary()}
}

// resultSummary is "12 api_invoke_endpoint results and 3 trino_query results",
// most first, or empty when the run was handed none.
func (m *Meter) resultSummary() string {
	if len(m.results) == 0 {
		return ""
	}
	tools := slices.Sorted(maps.Keys(m.results))
	slices.SortStableFunc(tools, func(a, b string) int { return m.results[b] - m.results[a] })
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		noun := "results"
		if m.results[tool] == 1 {
			noun = "result"
		}
		parts = append(parts, fmt.Sprintf("%d %s %s", m.results[tool], tool, noun))
	}
	return strings.Join(parts, " and ")
}
