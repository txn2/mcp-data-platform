package siterender

import (
	"context"
	"fmt"
	"reflect"
)

// Limits bound one render. A render is a single goroutine holding a single
// meter, so everything a template can spend is charged to one place.
type Limits struct {
	// Steps is the loop iterations and includes of the whole render.
	Steps int64
	// PageBytes is what one page may hold at once: its variables and output.
	PageBytes int64
	// TotalBytes is the output of the whole render.
	TotalBytes int64
	// Pages is how many pages the render may produce.
	Pages int
	// Depth is how deeply includes and layouts may nest.
	Depth int
	// Nesting is how deeply blocks may nest in one template.
	Nesting int
	// TemplateBytes is the largest template or record read.
	TemplateBytes int64
	// DataBytes is the records and front matter a render may hold at once.
	DataBytes int64
	// Failures is how many errors a render reports before stopping.
	Failures int
}

// The bounds a render takes when its Limits leave them unset.
const (
	defaultSteps         = int64(50_000_000)
	defaultPageBytes     = int64(64 << 20)
	defaultTotalBytes    = int64(2 << 30)
	defaultPages         = 20000
	defaultDepth         = 32
	defaultNesting       = 64
	defaultTemplateBytes = int64(4 << 20)
	defaultDataBytes     = int64(256 << 20)
	defaultFailures      = 20
	sliceElementBytes    = 16
	resourcePageBytes    = "bytes on one page"
)

func (l Limits) withDefaults() Limits {
	fill := func(v *int64, d int64) {
		if *v <= 0 {
			*v = d
		}
	}
	fillInt := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	fill(&l.Steps, defaultSteps)
	fill(&l.PageBytes, defaultPageBytes)
	fill(&l.TotalBytes, defaultTotalBytes)
	fill(&l.TemplateBytes, defaultTemplateBytes)
	fill(&l.DataBytes, defaultDataBytes)
	fillInt(&l.Pages, defaultPages)
	fillInt(&l.Depth, defaultDepth)
	fillInt(&l.Nesting, defaultNesting)
	fillInt(&l.Failures, defaultFailures)
	return l
}

// BudgetError is a render stopped for spending more than its limits allow.
type BudgetError struct {
	Resource string
	Limit    int64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("the render exceeded its limit of %d %s", e.Limit, e.Resource)
}

// meter is what the template engine's hooks charge. It belongs to one goroutine.
type meter struct {
	ctx    context.Context
	limits Limits
	steps  int64
	total  int64
	depth  int
	// page is the output of the current page; vars what its variables hold.
	page int64
	vars map[string]int64
	live int64
	// exceeded is the first limit crossed. The engine flattens a filter's error
	// into text, so this is what says a render ran out, and it stays set so
	// that nothing a template does afterward can spend more.
	exceeded *BudgetError
}

func newMeter(ctx context.Context, limits Limits) *meter {
	return &meter{ctx: ctx, limits: limits, vars: map[string]int64{}}
}

// startPage forgets what the last page held. A limit crossed stays crossed: a
// page that ran out is a failed page, and only a total limit ends the render.
func (m *meter) startPage() {
	m.page, m.live, m.depth = 0, 0, 0
	clear(m.vars)
	m.exceeded = nil
}

// step charges one iteration or include, and is where a render notices its
// deadline.
func (m *meter) step() error {
	m.steps++
	if m.steps > m.limits.Steps {
		return m.fail("steps", m.limits.Steps)
	}
	if m.exceeded != nil {
		return m.exceeded
	}
	return m.ctx.Err() //nolint:wrapcheck // the render reads it by identity
}

func (m *meter) fail(resource string, limit int64) error {
	if m.exceeded == nil {
		m.exceeded = &BudgetError{Resource: resource, Limit: limit}
	}
	return m.exceeded
}

func (m *meter) held() error {
	if m.page+m.live > m.limits.PageBytes {
		return m.fail(resourcePageBytes, m.limits.PageBytes)
	}
	return nil
}

// write charges output, wherever it is going: the page or a capture.
func (m *meter) write(n int) error {
	m.page += int64(n)
	return m.held()
}

// assign records what a variable now holds, in place of what it held.
func (m *meter) assign(name string, size int64) error {
	m.live += size - m.vars[name]
	m.vars[name] = size
	return m.held()
}

// produced refuses a value too large to build, before it is built.
func (m *meter) produced(size int64) error {
	if size > m.limits.PageBytes {
		return m.fail(resourcePageBytes, m.limits.PageBytes)
	}
	return nil
}

func (m *meter) enter() error {
	m.depth++
	if m.depth > m.limits.Depth {
		return m.fail("levels of include", int64(m.limits.Depth))
	}
	return m.step()
}

func (m *meter) leave() { m.depth-- }

// sizeOf is what a variable's value holds: its bytes for a string and its
// element headers for a list, whose strings it shares.
func sizeOf(v any) int64 {
	switch val := v.(type) {
	case nil:
		return 0
	case string:
		return int64(len(val))
	case []byte:
		return int64(len(val))
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice {
		return int64(rv.Len()) * sliceElementBytes
	}
	return 0
}
