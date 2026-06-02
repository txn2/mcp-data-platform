package apigateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RawSink receives an upstream response streamed by the raw passthrough
// path. The REST shim (pkg/gatewayhttp) adapts an http.ResponseWriter
// to this interface and installs it on the request context; the
// toolkit stays decoupled from the concrete HTTP writer and can be
// exercised with an in-memory sink in tests.
//
// Call order is AddHeader* then SetStatus then Write*: headers must be
// staged before the status line is flushed, exactly like
// http.ResponseWriter.
type RawSink interface {
	// AddHeader appends a response header value. Must be called before
	// SetStatus.
	AddHeader(key, value string)
	// SetStatus flushes the response status line. Idempotent: only the
	// first call has effect.
	SetStatus(code int)
	io.Writer
}

// RawPassthrough carries the sink and the all-or-nothing size limit for
// a single raw passthrough request. MaxBytes <= 0 means no size limit
// (memory stays bounded regardless because the body is streamed, never
// buffered).
type RawPassthrough struct {
	Sink     RawSink
	MaxBytes int64
}

type rawPassthroughKey struct{}

// WithRawPassthrough installs a raw passthrough request on the context.
// The REST shim sets this on the in-memory MCP session's connection
// context so api_invoke_endpoint's handler streams the upstream body to
// the sink instead of buffering and enveloping it. All auth, persona
// authorization, route policy, and audit middleware still run because
// the request flows through the normal MCP call path.
func WithRawPassthrough(ctx context.Context, rp *RawPassthrough) context.Context {
	return context.WithValue(ctx, rawPassthroughKey{}, rp)
}

// rawPassthroughFromContext returns the installed raw passthrough, or
// nil when the request is an ordinary buffered call.
func rawPassthroughFromContext(ctx context.Context) *RawPassthrough {
	rp, _ := ctx.Value(rawPassthroughKey{}).(*RawPassthrough)
	return rp
}

// rawResponseHeaders is the set of upstream response headers copied
// through to the raw client. Unlike the buffered path's
// passthroughResponseHeaders (tuned for what a model finds useful),
// this set is tuned for retrieving a file or binary object: content
// negotiation, the download filename, and cache validators.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var rawResponseHeaders = map[string]bool{
	"Content-Type":        true,
	"Content-Length":      true,
	"Content-Encoding":    true,
	"Content-Disposition": true,
	"Cache-Control":       true,
	"Etag":                true,
	"Last-Modified":       true,
}

// handleInvokeRaw streams the upstream response straight to the sink
// with bounded memory (issue #535). The route policy has already
// authorized the call in handleInvoke. The returned CallToolResult is:
//
//   - an error result (IsError) when the call fails BEFORE any bytes
//     are streamed (request build, transport, or a 413 size rejection)
//     so the REST shim can map it to the right HTTP status; OR
//   - a non-error sentinel once streaming has begun, because the HTTP
//     status and headers are already flushed and cannot be rewritten.
func (*Toolkit) handleInvokeRaw(ctx context.Context, c *conn, in InvokeInput, raw *RawPassthrough) (*mcp.CallToolResult, any, error) {
	timeout := resolveTimeout(in.TimeoutSeconds, c.cfg.CallTimeout)
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := buildUpstreamRequest(callCtx, c.cfg, c.auth, c.specs, in)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}

	// #nosec G107 G704 -- req.URL is host-pinned by buildURL + validatePath,
	// identical to executeRequest; the credential is injected by c.auth.Apply
	// inside buildRawRequest, satisfying the "gateway holds the credential"
	// constraint even on the streamed path.
	resp, err := c.client.Do(req)
	if err != nil {
		return errorResult(scrubTransportError(err)), nil, nil
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	limit := raw.MaxBytes
	if limit > 0 && resp.ContentLength > limit {
		slog.Warn("apigateway: rejecting raw passthrough, upstream body exceeds limit",
			logKeyConnection, in.Connection, "path", in.Path,
			"limit_bytes", limit, "actual_bytes", resp.ContentLength)
		return bodyTooLargeResult(in.Connection, in.Path, limit, resp.ContentLength), nil, nil
	}

	n, streamErr := streamRaw(raw.Sink, resp, limit)
	if streamErr != nil {
		// Headers + some bytes were already flushed, so the HTTP status
		// can no longer change. Log and return a non-error sentinel so
		// the REST shim does not attempt a second response write.
		slog.Warn("apigateway: raw passthrough stream interrupted",
			logKeyConnection, in.Connection, "path", in.Path,
			"bytes_streamed", n, logKeyError, streamErr)
	}
	return rawStreamedResult(in.Connection, resp.StatusCode, n), nil, nil
}

// streamRaw copies the upstream body to the sink with the upstream
// status and selected headers. When limit > 0 the copy is bounded by
// limit+1 bytes; reading past the limit means the upstream sent more
// than its declared (or undeclared, chunked) Content-Length suggested,
// which is reported as an error AFTER the headers were already flushed.
// Returns the number of bytes streamed.
func streamRaw(sink RawSink, resp *http.Response, limit int64) (int64, error) {
	copyRawHeaders(resp.Header, sink)
	sink.SetStatus(resp.StatusCode)
	var reader io.Reader = resp.Body
	// limit+1 detects an over-limit body; guard against int64 overflow
	// when an operator sets an effectively-unlimited cap (math.MaxInt64),
	// where limit+1 would wrap negative and LimitReader would yield zero
	// bytes. At that magnitude no real body can exceed it, so stream
	// unbounded.
	if limit > 0 && limit < math.MaxInt64 {
		reader = io.LimitReader(resp.Body, limit+1)
	}
	n, err := io.Copy(sink, reader)
	if err != nil {
		return n, fmt.Errorf("apigateway: streaming upstream response: %w", err)
	}
	if limit > 0 && n > limit {
		return n, fmt.Errorf("upstream body exceeded raw limit of %d bytes (chunked or undeclared length)", limit)
	}
	return n, nil
}

// copyRawHeaders forwards the rawResponseHeaders subset from the
// upstream response to the sink.
func copyRawHeaders(h http.Header, sink RawSink) {
	for name, values := range h {
		canonical := http.CanonicalHeaderKey(name)
		if !rawResponseHeaders[canonical] {
			continue
		}
		for _, v := range values {
			sink.AddHeader(canonical, v)
		}
	}
}

// rawStreamedResult is the non-error sentinel returned once a raw
// passthrough has begun streaming. The REST shim ignores its body when
// the sink already wrote a response; the audit middleware records it.
func rawStreamedResult(connection string, status int, n int64) *mcp.CallToolResult {
	return jsonResult(map[string]any{
		"raw_streamed":    true,
		"connection":      connection,
		"upstream_status": status,
		"bytes_streamed":  n,
	})
}
