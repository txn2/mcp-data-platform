// Package handlertransport serves an http.Client's requests from an
// in-process http.Handler instead of the network (#1005): the handler runs in
// a goroutine and its response body streams through a pipe, so everything
// downstream of client.Do (a buffered read, an export streamed to storage,
// size and timeout caps, audit, metrics) treats an in-process handler exactly
// like a network upstream. The api gateway's handler=internal connections are
// built on it.
package handlertransport

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
)

// intBase / int64Bits are the base and bitSize passed to strconv.ParseInt
// for a Content-Length (an int64).
const (
	intBase   = 10
	int64Bits = 64
)

// New is the transport that serves every request from h.
func New(h http.Handler) http.RoundTripper { return &internalRoundTripper{handler: h} }

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
				_ = pw.CloseWithError(fmt.Errorf("internal handler panic: %v", p))
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
