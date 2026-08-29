package pagewalk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrPageDoesNotFit is what a Sink returns when the page it was handed
// would take the merged result past the cap it holds. The walk stops
// with StoppedByMaxBytes and hands back the signal that led to the page,
// so the caller can resume from it.
var ErrPageDoesNotFit = errors.New("page does not fit under the byte cap")

// Sink receives the items of one page in order. Items are the raw bytes
// of each array element, so what is merged is what the upstream sent:
// no re-encoding, no number rounding.
type Sink func(items []json.RawMessage) error

// Page is one page as read: the status and headers the walk reports,
// the Content-Type, and the raw body.
type Page struct {
	Status      int
	Header      http.Header
	ContentType string
	Body        []byte
}

// Requester is what the gateway supplies to request and read a page.
// Do builds and sends the request for a Target, with every guard a
// single call gets; ReadPage buffers a 2xx answer under the caps a
// single call has, and fails any other status.
type Requester interface {
	Do(ctx context.Context, target Target) (*http.Response, error)
	ReadPage(resp *http.Response) (Page, error)
}

// Options is what New builds a Walk from.
type Options struct {
	Paginate  PaginateInput
	Address   AddressSpec
	Requester Requester
	// Authorize is the route policy check run on every page's target.
	// nil when the deployment installed no policy.
	Authorize func(Target) error
	Sink      Sink
	// Now is the clock Retry-After dates are read against; nil is
	// time.Now.
	Now func() time.Time
}

// Walk is the state of one walk. After Run, Stats, Resume, Lead and Last
// are what the caller reports.
type Walk struct {
	paginate  PaginateInput
	itemsPath []string
	addr      Address
	req       Requester
	authorize func(Target) error
	sink      Sink
	now       func() time.Time

	// Stats is the page count, item count, and why the walk stopped.
	Stats WalkStats
	// Resume is the signal for the page after the last merged one when
	// the walk stopped early (max_pages or max_bytes); nil at the end.
	Resume *PaginationInfo
	// Lead is the signal that addressed the most recent page: the walk's
	// "final cursor" for provenance.
	Lead *PaginationInfo
	// Last is the last page read, whose status and headers the inline
	// output reports as a single call's would be.
	Last Page
}

// New validates the paginate block against the request and binds the
// address the walk moves.
func New(opts Options) (*Walk, error) {
	p, err := opts.Paginate.normalize(opts.Address.Query)
	if err != nil {
		return nil, err
	}
	addr, err := NewAddress(opts.Address)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Walk{
		paginate: p, itemsPath: p.itemsPath(), addr: addr,
		req: opts.Requester, authorize: opts.Authorize, sink: opts.Sink, now: now,
	}, nil
}

// Run walks the pages until the end, a bound, or a failure. A failure
// names the page: the walk's caller reports it whole, and a partial
// result is never returned as a success.
func (w *Walk) Run(ctx context.Context) error {
	for {
		pageNo := w.Stats.PagesFetched + 1
		stop, err := w.step(ctx, pageNo)
		if err != nil {
			return fmt.Errorf("page %d: %w", pageNo, err)
		}
		if stop {
			return nil
		}
	}
}

// step fetches one page, merges it, and moves the address to the next.
// It reports whether the walk is over.
func (w *Walk) step(ctx context.Context, pageNo int) (stop bool, err error) {
	target := w.addr.Target()
	if w.authorize != nil {
		if err := w.authorize(target); err != nil {
			return true, err
		}
	}
	page, err := w.fetchPage(ctx, target)
	if err != nil {
		return true, err
	}
	items, err := extractItems(page.Body, w.itemsPath)
	if err != nil {
		return true, err
	}
	if err := w.sink(items); err != nil {
		if errors.Is(err, ErrPageDoesNotFit) {
			w.Stats.StoppedBy, w.Resume = StoppedByMaxBytes, w.Lead
			return true, nil
		}
		return true, err
	}
	w.Stats.PagesFetched++
	w.Stats.ItemsMerged += len(items)
	if len(items) == 0 {
		w.Stats.StoppedBy = StoppedByEnd
		return true, nil
	}
	return w.advance(page, pageNo)
}

// advance reads the page's next signal and moves the address, or ends
// the walk when there is none or the page bound is reached.
func (w *Walk) advance(page Page, pageNo int) (stop bool, err error) {
	sig := Detect(page.Header, parseJSON(page.Body))
	next, err := nextPage(w.addr, w.paginate, sig, pageNo)
	if err != nil {
		return true, err
	}
	if next == nil {
		w.Stats.StoppedBy = StoppedByEnd
		return true, nil
	}
	if w.Stats.PagesFetched >= w.paginate.MaxPages {
		w.Stats.StoppedBy, w.Resume = StoppedByMaxPages, next
		return true, nil
	}
	w.Lead = next
	return false, nil
}

// parseJSON decodes a page body for signal detection; a body that is
// not JSON detects nothing from the body (the Link header still counts).
func parseJSON(body []byte) any {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil
	}
	return v
}

// maxRetryAfterPauses bounds how many times one page is retried on a
// 429 or 503 with Retry-After. The pause is the upstream's own interval,
// so the bound is not a pacing rule; it keeps an upstream that answers
// every request with a zero interval from being polled in a tight loop
// until the call's timeout.
const maxRetryAfterPauses = 10

// fetchPage requests the target until it answers with a page. A 429 or
// 503 carrying Retry-After pauses the walk for that interval, bounded by
// the call's timeout, and the same page is requested again; any other
// non-2xx answer fails the page.
func (w *Walk) fetchPage(ctx context.Context, target Target) (Page, error) {
	for pauses := 0; ; pauses++ {
		resp, err := w.req.Do(ctx, target)
		if err != nil {
			return Page{}, err //nolint:wrapcheck // Run names the page; the requester's text is the cause
		}
		wait, retry := retryAfterPause(resp, w.now())
		if !retry {
			page, err := w.req.ReadPage(resp)
			if err == nil {
				w.Last = page
			}
			return page, err //nolint:wrapcheck // Run names the page; the requester's text is the cause
		}
		_ = resp.Body.Close() // the body of a refusal is not read
		if pauses >= maxRetryAfterPauses {
			return Page{}, fmt.Errorf("upstream returned %d with Retry-After %d times in a row", resp.StatusCode, pauses+1)
		}
		if err := waitRetryAfter(ctx, wait); err != nil {
			return Page{}, err
		}
	}
}

// extractItems returns the raw elements of the array at path in a page
// body. A key absent along the path is a page with no items (an
// upstream that omits the array on its last page ends the walk
// cleanly); a present value that is not an array is a wrong `items`
// and fails the page, because merging it would produce a result that
// is not the collection the caller asked for.
func extractItems(body []byte, path []string) ([]json.RawMessage, error) {
	raw := json.RawMessage(body)
	for i, key := range path {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, fmt.Errorf("body is not a JSON object at %q", strings.Join(path[:i+1], "."))
		}
		next, ok := obj[key]
		if !ok {
			return nil, nil
		}
		raw = next
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		where := itemsRootPath
		if len(path) > 0 {
			where = strings.Join(path, ".")
		}
		return nil, fmt.Errorf("items at %q is not a JSON array", where)
	}
	return items, nil
}
