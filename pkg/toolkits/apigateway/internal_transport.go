package apigateway

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// This file implements the handler=internal dispatch (issue #1005):
// a connection whose http.Client round-trips into an in-process
// http.Handler instead of dialing an upstream. The seam sits at the
// transport so everything downstream of client.Do — the buffered
// invoke read, the export stream-to-S3 path, size and timeout caps,
// audit, metrics instrumentation — treats an internal connection
// exactly like a network one.

// int64Bits is the bitSize passed to strconv.ParseInt for a
// Content-Length (an int64).
const int64Bits = 64

// SetInternalHandler wires the in-process handler that
// handler=internal connections dispatch to. Must be called before
// such a connection is added; AddConnection fails otherwise, because
// a connection that can never serve a request should not register.
func (t *Toolkit) SetInternalHandler(h http.Handler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.internalHandler = h
}

// newInternalHTTPClient builds the dispatch client for a
// handler=internal connection. Unlike newHTTPClient it sets NO
// client-level Timeout: the per-call context already bounds every
// path (api_invoke_endpoint via resolveTimeout, api_export via
// resolveExportTimeout), and http.Client.Timeout also bounds body
// READING — so a client timeout of cfg.CallTimeout (60s default)
// would cap a large fetch_url export at the call timeout and defeat
// api_export's own multi-minute timeout. Bounding through the context
// lets an export run for its configured export timeout while an inline
// invoke stays bounded by the shorter invoke timeout. Redirects are
// not followed here; the internal handler owns its own redirect policy
// (fetch_url's follow_redirects) and returns a final response.
func newInternalHTTPClient(h http.Handler) *http.Client {
	return &http.Client{
		Transport: &internalRoundTripper{handler: h},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// internalRoundTripper serves a request by running an http.Handler in
// a goroutine and streaming its response body through a pipe. The
// response is returned as soon as the handler commits its header, so
// a large body (an export streaming to S3) flows through without ever
// being buffered whole.
type internalRoundTripper struct {
	handler http.Handler
}

// RoundTrip implements http.RoundTripper. Closing the returned Body
// aborts the handler's writes (pipe closed), which cancels any
// outbound work tied to the request context — the same semantics a
// network transport gives a caller that abandons a response.
func (rt *internalRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	w := newInternalResponseWriter(pw)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				// Match net/http server semantics: a panicking
				// handler yields an aborted response, not a
				// process crash. If the header was not committed
				// yet, commit a 500 so RoundTrip can return.
				w.WriteHeader(http.StatusInternalServerError)
				_ = pw.CloseWithError(fmt.Errorf("apigateway: internal handler panic: %v", p))
				return
			}
			// A handler that returned without writing anything
			// still owes a response: commit the implicit 200.
			w.WriteHeader(http.StatusOK)
			_ = pw.Close() // io.PipeWriter.Close never returns an error
		}()
		rt.handler.ServeHTTP(w, req)
	}()

	select {
	case <-w.headerDone:
	case <-req.Context().Done():
		_ = pr.CloseWithError(req.Context().Err())
		return nil, req.Context().Err() //nolint:wrapcheck // transport contract: return ctx error as-is
	}

	status, header := w.committed()
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          pr,
		ContentLength: contentLengthFromHeader(header),
		Request:       req,
	}, nil
}

// contentLengthFromHeader parses a declared Content-Length, returning
// -1 (unknown, the http.Response convention) when absent or
// unparseable. The export path's early over-cap rejection reads
// Response.ContentLength, so propagating the handler's declaration
// keeps that check working for internal connections.
func contentLengthFromHeader(h http.Header) int64 {
	v := h.Get("Content-Length")
	if v == "" {
		return -1
	}
	n, err := strconv.ParseInt(v, intBase, int64Bits)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

// internalResponseWriter adapts the pipe to http.ResponseWriter.
// WriteHeader commits the status and a snapshot of the header map
// exactly once (later calls are no-ops, matching net/http), and
// signals headerDone so RoundTrip can hand the response to the
// caller while the body is still streaming.
type internalResponseWriter struct {
	pw *io.PipeWriter

	mu          sync.Mutex
	header      http.Header
	status      int
	snapshot    http.Header
	wroteHeader bool
	headerDone  chan struct{}
}

func newInternalResponseWriter(pw *io.PipeWriter) *internalResponseWriter {
	return &internalResponseWriter{
		pw:         pw,
		header:     make(http.Header),
		headerDone: make(chan struct{}),
	}
}

// Header implements http.ResponseWriter.
func (w *internalResponseWriter) Header() http.Header { return w.header }

// WriteHeader implements http.ResponseWriter. First call wins; the
// header map is cloned at commit time so handler mutations after
// WriteHeader (legal but ignored by net/http too) cannot race the
// reader.
func (w *internalResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.snapshot = w.header.Clone()
	close(w.headerDone)
}

// Write implements http.ResponseWriter, committing the implicit 200
// on first write like net/http.
func (w *internalResponseWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b) //nolint:wrapcheck // transparent pipe pass-through
}

// committed returns the committed status and header snapshot. Only
// valid after headerDone is closed; the channel close orders the
// field writes before this read.
func (w *internalResponseWriter) committed() (int, http.Header) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, w.snapshot
}

// CloseWithError fails the response body so a reader sees err instead
// of a clean EOF. Used by a handler that has already committed its
// status (so the outcome cannot be re-signaled as an error status) but
// then hits a mid-stream failure — most importantly a fetch_url
// upstream that truncates, where the export path must abort its S3
// upload rather than persist a partial asset. The status is committed
// first so RoundTrip has returned a response before the body errors;
// the goroutine's deferred pw.Close() that follows is a no-op because
// io.PipeWriter keeps the first close error.
func (w *internalResponseWriter) CloseWithError(err error) {
	w.WriteHeader(http.StatusOK)
	_ = w.pw.CloseWithError(err) // io.PipeWriter.CloseWithError never returns an error
}
