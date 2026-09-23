package scriptguard

import (
	"fmt"
	"maps"
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

// walkEvery is how often the meter walks everything a run holds. Between
// walks it adds each result a host call hands the script to the last walk's
// total, which can only overstate what the run holds; a total over the budget
// is confirmed by a fresh walk before the run is failed. Walking a large heap
// at every call of a tight loop would cost more than the calls.
const walkEvery = time.Second

// Meter measures what one run holds against its budget and keeps the peak.
// It is used from the interpreter's goroutine only, at host calls.
type Meter struct {
	budget   int64
	peak     int64
	estimate int64
	// fresh is set while the estimate is a walk with nothing added since.
	fresh bool
	// held is memory the run holds outside the interpreter -- the pages of
	// an appended output -- which no walk of its values reaches.
	held   int64
	walked time.Time
	now    func() time.Time
	// results counts the tool results the run has been handed, by tool, for
	// the refusal to say where the memory came from.
	results map[string]int
}

// NewMeter returns a meter for a run allowed budget bytes; zero or less sets
// no budget and still measures the peak.
func NewMeter(budget int64) *Meter {
	return &Meter{budget: budget, now: time.Now, results: map[string]int{}}
}

// Peak is the most the run was measured holding.
func (m *Meter) Peak() int64 {
	if m == nil {
		return 0
	}
	return m.peak
}

// Check measures what the thread holds at the host call named at, walking it
// when the last walk is older than walkEvery, and refuses once it is over the
// budget. A nil meter measures nothing.
func (m *Meter) Check(thread *starlark.Thread, at string) error {
	if m == nil || thread == nil {
		return nil
	}
	if m.walked.IsZero() || m.now().Sub(m.walked) >= walkEvery {
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
	if m.within() {
		return nil
	}
	// Over on the running estimate: confirm with a walk, which the result is
	// not yet part of, before failing the run.
	m.walk(thread)
	m.add(size)
	if m.within() {
		return nil
	}
	return m.refusal(at)
}

// Settle records what the module's globals hold when the script ends, so the
// peak includes what it built after its last host call.
func (m *Meter) Settle(globals starlark.StringDict) {
	if m == nil {
		return
	}
	w := walker{seen: map[any]bool{}}
	for _, v := range globals {
		w.value(v)
	}
	m.peak = max(m.peak, w.total)
}

// walk replaces the estimate with a fresh measure of what the thread holds.
func (m *Meter) walk(thread *starlark.Thread) {
	w := walker{seen: map[any]bool{}}
	if thread != nil {
		w.held(thread)
	}
	m.estimate, m.fresh, m.walked = w.total+m.held, true, m.now()
	m.peak = max(m.peak, m.estimate)
}

// Holding records how much the run holds outside the interpreter, replacing
// the last figure.
func (m *Meter) Holding(n int64) {
	if m == nil {
		return
	}
	m.estimate += n - m.held
	m.held = n
	m.peak = max(m.peak, m.estimate)
}

// add grows the estimate by a value the run is about to hold.
func (m *Meter) add(size int64) {
	m.estimate += size
	m.fresh = false
	m.peak = max(m.peak, m.estimate)
}

// within reports whether the estimate is inside the budget, or there is none.
func (m *Meter) within() bool { return m.budget <= 0 || m.estimate <= m.budget }

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
