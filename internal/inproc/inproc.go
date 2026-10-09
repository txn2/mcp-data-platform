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
	"net/http"
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
func Read(routes http.Handler, path string, header http.Header, timeout time.Duration) (Response, bool) {
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
	rec := NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code() != http.StatusOK && rec.Code() != http.StatusPartialContent {
		return Response{}, false
	}
	return Response{Status: rec.Code(), Header: rec.Header(), Body: rec.Body.Bytes()}, true
}

// Recorder is a response a route writes into in-process.
type Recorder struct {
	header http.Header
	status int
	// Body is what the route wrote.
	Body bytes.Buffer
}

// NewRecorder returns an empty response.
func NewRecorder() *Recorder { return &Recorder{header: http.Header{}} }

// Header is the response header the route sets.
func (r *Recorder) Header() http.Header { return r.header }

// WriteHeader records the first status the route writes.
func (r *Recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// Write collects the body, implying 200 when no status was written.
func (r *Recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.Body.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

// Code is the status the route answered, 200 when it wrote none.
func (r *Recorder) Code() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
