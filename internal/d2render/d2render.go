// Package d2render renders a D2 diagram source to SVG in the platform
// (#2302), with D2's own Go library and its dagre layout.
//
// It renders on the server because D2's browser build loads its layout engine
// with new Function in a blob: worker, which the share viewer's policy does
// not allow, and the same SVG then serves the asset page, both kinds of share
// link, the tile and the PDF alike. A source is held to the save-time check
// (internal/sourcecheck) before it is stored, so the render sees no import and
// no URL other than a managed resource's.
//
// A render runs dagre's JavaScript inside goja, the Go JavaScript runtime D2
// uses; the diagram source is never script. Each render is bounded by the
// source size, a timeout and how many run at once, and a rendered diagram is
// kept by its source's hash so a page viewed again does not render again.
package d2render

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2dagrelayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	"oss.terrastruct.com/d2/lib/log"
	"oss.terrastruct.com/d2/lib/textmeasure"
)

const (
	// MaxSourceBytes is the largest source rendered.
	MaxSourceBytes = 256 << 10
	// MaxSVGBytes is the largest drawing returned.
	MaxSVGBytes = 16 << 20
	// Timeout bounds one render.
	Timeout = 10 * time.Second
	// concurrent is how many renders run at once in one process.
	concurrent = 2
	// cached is how many drawings are kept.
	cached = 64
)

// ErrTooLarge is returned for a source or drawing past its bound.
var ErrTooLarge = errors.New("d2render: the diagram is too large to render")

// ErrTimeout is returned for a render that did not finish in time.
var ErrTimeout = errors.New("d2render: the diagram did not finish rendering in time")

var (
	slots = make(chan struct{}, concurrent)
	cache = newLRU(cached)
)

// Render returns the SVG drawing of src, in D2's default light theme or its
// dark one.
func Render(ctx context.Context, src string, dark bool) ([]byte, error) {
	if len(src) > MaxSourceBytes {
		return nil, ErrTooLarge
	}
	key := fmt.Sprintf("%x:%t", sha256.Sum256([]byte(src)), dark)
	if svg, ok := cache.get(key); ok {
		return svg, nil
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrTimeout
	}
	type result struct {
		svg []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		// The slot is held until the render returns, timed out or not, so a
		// render that outlives its caller still counts against the bound.
		defer func() { <-slots }()
		svg, err := draw(ctx, src, dark)
		done <- result{svg, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		if len(r.svg) > MaxSVGBytes {
			return nil, ErrTooLarge
		}
		cache.put(key, r.svg)
		return r.svg, nil
	case <-ctx.Done():
		return nil, ErrTimeout
	}
}

// draw compiles, lays out and draws one source.
func draw(ctx context.Context, src string, dark bool) ([]byte, error) {
	ruler, err := textmeasure.NewRuler()
	if err != nil {
		return nil, fmt.Errorf("d2render: loading fonts: %w", err)
	}
	layout := func(ctx context.Context, g *d2graph.Graph) error { return d2dagrelayout.Layout(ctx, g, nil) }
	// D2 logs through the context's logger, and what it logs can quote the
	// diagram. It is discarded, so a source never reaches the platform's logs.
	ctx = log.With(ctx, slog.New(slog.DiscardHandler))
	diagram, _, err := d2lib.Compile(ctx, src, &d2lib.CompileOptions{
		Ruler:          ruler,
		LayoutResolver: func(string) (d2graph.LayoutGraph, error) { return layout, nil },
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("d2render: %s", strings.TrimSpace(err.Error()))
	}
	theme := d2themescatalog.NeutralDefault.ID
	if dark {
		theme = d2themescatalog.DarkMauve.ID
	}
	pad := int64(d2svg.DEFAULT_PADDING)
	svg, err := d2svg.Render(diagram, &d2svg.RenderOpts{Pad: &pad, ThemeID: &theme})
	if err != nil {
		return nil, fmt.Errorf("d2render: drawing: %w", err)
	}
	return svg, nil
}

// lru is a small least-recently-used map of drawings.
type lru struct {
	mu    sync.Mutex
	limit int
	order []string
	items map[string][]byte
}

func newLRU(limit int) *lru { return &lru{limit: limit, items: make(map[string][]byte, limit)} }

func (c *lru) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	if ok {
		c.touch(key)
	}
	return v, ok
}

func (c *lru) put(key string, v []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok && len(c.items) >= c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
	c.items[key] = v
	c.touch(key)
}

// touch moves key to the most recent end.
func (c *lru) touch(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
}
