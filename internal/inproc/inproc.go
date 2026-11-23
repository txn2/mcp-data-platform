// Package inproc answers a GET against the platform's own routes inside the
// process, as an anonymous caller on loopback.
//
// A document drawn in the headless renderer loads its references and the
// served slide runtime from the platform's own origin, and the renderer has no
// route to the platform: every request it makes is answered by the platform.
// The tile worker and the PDF export both answer those requests here, so the
// page gets what a reader's browser would get from the same route and nothing
// a session would add.
package inproc

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/viewerlimit"
)

// forwardedHeaders are the request headers Read passes to the route: the ones
// that change what a public route answers. A ranged read is how a map reads
// its basemap (#2068), and Origin is what a CORS answer is made for.
var forwardedHeaders = []string{"Range", "If-Range", "If-None-Match", "Origin"}

// Response is what a route answered a Read with.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Read calls routes for one GET of path, passing on the headers of header
// that change a public route's answer, and returns the response when the
// route answered 200 or, for a ranged read, 206. It carries no credentials,
// so only a route that answers anonymously can answer it.
//
// It is marked as the platform's own request, which the public viewer's rate
// limiter admits without counting (#1791). Counted, every request presents the
// one loopback address and shares one bucket, and the reference route in
// front of a document's files ran it dry partway through a document.
//
// The body is held to limit bytes (#2721): a route answering more is stopped
// where it passes it, and Read reports 413 rather than the part it wrote.
func Read(routes http.Handler, path string, header http.Header, timeout time.Duration, limit int64) (Response, bool) {
	ctx, cancel := context.WithTimeout(viewerlimit.InProcess(context.Background()), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
	if err != nil {
		return Response{}, false
	}
	for _, h := range forwardedHeaders {
		if v := header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	req.RemoteAddr = "127.0.0.1:0"
	rec := NewRecorder(limit)
	routes.ServeHTTP(rec, req)
	if rec.Over() {
		return Response{Status: http.StatusRequestEntityTooLarge}, false
	}
	if rec.Code() != http.StatusOK && rec.Code() != http.StatusPartialContent {
		// The status is kept so a caller can tell a file that is not there
		// from one the platform could not serve in time (#2218).
		status := rec.Code()
		if status == 0 || ctx.Err() != nil {
			status = http.StatusServiceUnavailable
		}
		return Response{Status: status}, false
	}
	return Response{Status: rec.Code(), Header: rec.Header(), Body: rec.Body.Bytes()}, true
}

// decimal is the base a Content-Length is written in, and int64Bits the
// width it is read into.
const (
	decimal   = 10
	int64Bits = 64
)

// maxPresize is the most a body is made room for up front from its declared
// length; a larger one within the limit grows as it is written.
const maxPresize = 64 << 20

// ErrTooLarge is what a Recorder's Write returns once the body would pass its
// limit, so the route stops writing.
var ErrTooLarge = errors.New("inproc: the response is larger than the reader takes")

// Recorder is a response a route writes into in-process.
type Recorder struct {
	header http.Header
	status int
	limit  int64
	over   bool
	// Body is what the route wrote, never more than the limit.
	Body bytes.Buffer
}

// NewRecorder returns an empty response holding at most limit body bytes.
func NewRecorder(limit int64) *Recorder { return &Recorder{header: http.Header{}, limit: limit} }

// Header is the response header the route sets.
func (r *Recorder) Header() http.Header { return r.header }

// WriteHeader records the first status the route writes. A body the route
// declares larger than the limit is refused here, before a byte of it is read
// (#2721), and one it declares within the limit is made room for once.
func (r *Recorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	n, err := strconv.ParseInt(r.header.Get("Content-Length"), decimal, int64Bits)
	switch {
	case err != nil || n <= 0:
	case n > r.limit:
		r.over = true
	case n <= maxPresize:
		r.Body.Grow(int(n))
	}
}

// Write collects the body, implying 200 when no status was written. A write
// that would take the body past the limit is refused whole, and the response
// is marked over.
func (r *Recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.over || int64(r.Body.Len())+int64(len(p)) > r.limit {
		r.over = true
		return 0, ErrTooLarge
	}
	return r.Body.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

// Over reports whether the route wrote more than the limit.
func (r *Recorder) Over() bool { return r.over }

// Code is the status the route answered, 200 when it wrote none.
func (r *Recorder) Code() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
