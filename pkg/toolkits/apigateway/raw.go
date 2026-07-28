package apigateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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

// rawForwardedHeaders is the set of upstream response headers copied
// verbatim to the raw client: body framing and cache validators, none of
// which carries a decision about how the bytes are rendered.
//
// Content-Range belongs to framing: a caller reaches a partial body by
// putting Range in the call's headers, and without the upstream's
// Content-Range the 206 that comes back names neither which bytes it holds
// nor how many there are in total, which makes the partial response
// unusable rather than merely undecorated. Accept-Ranges is deliberately
// not forwarded with it: this route answers a POST and does not honor a
// transport-level Range header on its own request, so advertising range
// support at that level would describe something the route does not do.
//
// Content-Type and Content-Disposition are also deliberately absent. They
// are the two headers that decide whether a browser turns the response into
// a document on the platform's origin and under what filename, and an
// upstream names both. copyRawHeaders derives them through
// blobserve.Headers instead, the same contract every other byte-serving
// surface in the platform answers under.
//
//nolint:gochecknoglobals // intentionally a package-level constant set
var rawForwardedHeaders = map[string]bool{
	"Content-Length":   true,
	"Content-Encoding": true,
	"Content-Range":    true,
	"Cache-Control":    true,
	"Etag":             true,
	"Last-Modified":    true,
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

	req, err := buildUpstreamRequest(callCtx, c.cfg, c.auth, catalogView{specs: c.specs, webdavRoutes: c.webdavRoutes()}, in)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	// #nosec G107 G704 -- req.URL is host-pinned by buildURL + validatePath,
	// identical to executeRequest; the credential is injected by c.auth.Apply
	// inside buildRawRequest, satisfying the "gateway holds the credential"
	// constraint even on the streamed path.
	resp, err := c.client.Do(req)
	if err != nil {
		return toolkit.ErrorResult(scrubTransportError(err)), nil, nil
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

// copyRawHeaders writes the raw response's headers to the sink: the
// upstream values that carry no rendering decision, forwarded verbatim,
// plus the blobserve header contract derived from the upstream's declared
// type and disposition.
//
// A raw passthrough reproduces upstream bytes on the platform's own
// origin, so it answers under the same contract as stored content: a
// scriptable type is served as an attachment, the type is reduced to a
// parsed media type, the sandbox CSP and nosniff are unconditional, and
// the filename is sanitized. Cache-Control is the one default the upstream
// may override, because an upstream that states its own cacheability has
// said something the platform has no better answer for; when it states
// nothing the platform writes `private` rather than leave an authorized
// response with no directive at all. That default is defense in depth here
// rather than a closed bypass: this route answers a POST, which a shared
// cache has no ordinary reason to store.
func copyRawHeaders(h http.Header, sink RawSink) {
	for name, values := range h {
		canonical := http.CanonicalHeaderKey(name)
		if !rawForwardedHeaders[canonical] {
			continue
		}
		for _, v := range values {
			sink.AddHeader(canonical, v)
		}
	}
	for name, values := range rawContentHeaders(h) {
		for _, v := range values {
			sink.AddHeader(name, v)
		}
	}
	if h.Get("Cache-Control") == "" {
		sink.AddHeader("Cache-Control", blobserve.DefaultCacheControl)
	}
}

// rawContentHeaders is the blobserve header contract for an upstream
// response, with the multipart boundary restored when the upstream answered
// a multi-range request.
//
// The boundary is the one Content-Type parameter this path keeps. A
// multipart/byteranges body is self-describing only through it, so dropping
// it turns a response the caller could parse into one it cannot, and unlike
// charset it carries no rendering decision: no browser builds a document
// from multipart/byteranges. It is re-emitted only when it matches the RFC
// 2046 boundary grammar, so an upstream value can never reach the header
// carrying a quote, semicolon or control character.
func rawContentHeaders(h http.Header) http.Header {
	headers := blobserve.Headers(rawContentOptions(h))
	ct := headers.Get(headerContentType)
	if ct != "multipart/byteranges" {
		return headers
	}
	_, params, err := mime.ParseMediaType(h.Get(headerContentType))
	if err != nil || !boundaryRe.MatchString(params["boundary"]) {
		return headers
	}
	headers.Set(headerContentType, mime.FormatMediaType(ct, map[string]string{"boundary": params["boundary"]}))
	return headers
}

// boundaryRe matches the RFC 2046 multipart boundary grammar, minus the space
// that grammar also allows: a boundary needing quotes is dropped rather than
// quoted, which keeps the emitted parameter a bare token.
//
//nolint:gochecknoglobals // compiled once, immutable
var boundaryRe = regexp.MustCompile(`^[0-9A-Za-z'()+_,./:=?-]{1,70}$`)

// rawContentOptions describes the upstream response to blobserve: its
// declared content type, and the filename and attachment intent recovered
// from its Content-Disposition. The recovered filename is untrusted and is
// sanitized by blobserve, so a quote, backslash or control character in it
// cannot close the quoted parameter or start a second header. A
// disposition that does not parse contributes neither, since a value that
// is not a media type carries no intent worth honoring.
func rawContentOptions(h http.Header) blobserve.Options {
	opts := blobserve.Options{ContentType: h.Get(headerContentType)}
	kind, params, err := mime.ParseMediaType(h.Get("Content-Disposition"))
	if err != nil {
		return opts
	}
	// ParseMediaType lowercases the type, so this comparison is exact.
	opts.Name, opts.ForceAttachment = params["filename"], kind == "attachment"
	return opts
}

// rawStreamedResult is the non-error sentinel returned once a raw
// passthrough has begun streaming. The REST shim ignores its body when
// the sink already wrote a response; the audit middleware records it.
func rawStreamedResult(connection string, status int, n int64) *mcp.CallToolResult {
	return toolkit.JSONResult(map[string]any{
		"raw_streamed":    true,
		"connection":      connection,
		"upstream_status": status,
		"bytes_streamed":  n,
	})
}
