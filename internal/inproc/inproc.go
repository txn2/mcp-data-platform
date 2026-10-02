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

// Get calls routes for one GET of path and returns the body and its content
// type when the route answers 200. It carries no credentials, so only a route
// that answers anonymously can answer it.
//
// It is marked as the platform's own request, which the public viewer's rate
// limiter admits without counting (#1791). Counted, every request presents the
// one loopback address and shares one bucket, and the reference route in
// front of a document's files ran it dry partway through a document.
func Get(routes http.Handler, path string, timeout time.Duration) (body []byte, contentType string, ok bool) {
	ctx, cancel := context.WithTimeout(viewerlimit.InProcess(context.Background()), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
	if err != nil {
		return nil, "", false
	}
	req.RemoteAddr = "127.0.0.1:0"
	rec := NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code() != http.StatusOK {
		return nil, "", false
	}
	return rec.Body.Bytes(), rec.Header().Get("Content-Type"), true
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
