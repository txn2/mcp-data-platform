package utilhandler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	// maxFetchRequestBytes bounds the fetch_url request document. The
	// document is a URL plus a small header map; 64 KiB is far above any
	// legitimate use (presigned URLs run a few KiB) and keeps the decode
	// allocation bounded.
	maxFetchRequestBytes = 64 * 1024

	// maxCustomHeaders bounds the opt-in header map.
	maxCustomHeaders = 32

	// maxRedirects bounds a follow_redirects chain. Matches net/http's
	// own default; made explicit so the limit is a documented contract
	// rather than an implementation coincidence.
	maxRedirects = 10

	// fetchUserAgent identifies the platform on outbound fetches when
	// the caller did not set a User-Agent of their own.
	fetchUserAgent = "mcp-data-platform/util-fetch"
)

// fetchRequest is the JSON body of POST /util/fetch. The URL is used
// exactly as given — no base_url join, no query re-encoding — so
// presigned-URL signatures survive intact. No credentials are injected:
// signed URLs carry their own in the query string, and headers are
// opt-in only.
type fetchRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// FollowRedirects defaults to true (nil = follow). Every redirect
	// hop re-enters the SSRF dial guard.
	FollowRedirects     *bool  `json:"follow_redirects,omitempty"`
	ExpectedContentType string `json:"expected_content_type,omitempty"`
}

func (in fetchRequest) followRedirects() bool {
	return in.FollowRedirects == nil || *in.FollowRedirects
}

// disallowedFetchHeaders are request headers the caller may not
// override: they are owned by the transport (Host routes the request,
// Content-Length/Transfer-Encoding frame it, Connection is hop-by-hop).
//
//nolint:gochecknoglobals // immutable constant set
var disallowedFetchHeaders = map[string]bool{
	"host":              true,
	"content-length":    true,
	"transfer-encoding": true,
	"connection":        true,
}

// handleFetch implements fetch_url. The response relays the fetched
// status, Content-Type, and body verbatim; the gateway's normal caps
// apply downstream (inline truncation for api_invoke_endpoint, the
// export byte cap for api_export).
func (h *handler) handleFetch(w http.ResponseWriter, r *http.Request) {
	var in fetchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxFetchRequestBytes)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid fetch request body: %v", err))
		return
	}
	req, err := buildOutboundRequest(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := h.newClient(in.followRedirects()).Do(req)
	if err != nil {
		writeFetchFailure(w, req.URL, err)
		return
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup
	if msg := expectedContentTypeMismatch(in.ExpectedContentType, resp.Header.Get(contentTypeHeader)); msg != "" {
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	relayResponse(w, resp, req.URL)
}

// buildOutboundRequest validates the fetch input and assembles the
// outbound request. The raw URL string is handed to net/http as-is so
// the query survives byte-for-byte (RawQuery is never re-encoded).
func buildOutboundRequest(ctx context.Context, in fetchRequest) (*http.Request, error) {
	method, err := validateFetchMethod(in.Method)
	if err != nil {
		return nil, err
	}
	if err := validateFetchURL(in.URL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, in.URL, http.NoBody)
	if err != nil {
		// Do NOT wrap err: net/http's *url.Error embeds the full URL
		// (query string and presigned signature included) in its
		// Error() text. validateFetchURL already parsed the URL, so a
		// failure here is a rare malformed-request edge; a fixed
		// message keeps the signature out of the response body.
		return nil, errors.New("url is not a valid request target")
	}
	if err := applyFetchHeaders(req, in.Headers); err != nil {
		return nil, err
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", fetchUserAgent)
	}
	return req, nil
}

// validateFetchMethod restricts fetch_url to read-only methods. An
// empty method defaults to GET. Side-effectful methods belong to a
// future, separately gated util operation (see issue #1005,
// util.http_post).
func validateFetchMethod(method string) (string, error) {
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		return http.MethodGet, nil
	}
	if m != http.MethodGet && m != http.MethodHead {
		return "", fmt.Errorf("method %q not supported (fetch_url is read-only: GET or HEAD)", m)
	}
	return m, nil
}

// validateFetchURL enforces the shape rules the dial guard cannot see:
// scheme (the guard only sees dials, so a file:// or ftp:// URL must
// be refused here) and embedded userinfo (a credential-in-URL is both
// a leak vector and the classic host-confusion trick).
func validateFetchURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// url.Parse's error embeds the raw input (query + signature).
		// Return a fixed message so a malformed presigned URL cannot
		// leak its credential into the response body or audit trail.
		return errors.New("url is not a valid absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme %q not supported (http or https)", u.Scheme)
	}
	if u.User != nil {
		return errors.New("url must not contain userinfo (user:password@)")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	return nil
}

// applyFetchHeaders applies the opt-in header map, refusing
// transport-owned headers and smuggling vectors.
func applyFetchHeaders(req *http.Request, headers map[string]string) error {
	if len(headers) > maxCustomHeaders {
		return fmt.Errorf("too many headers (%d, max %d)", len(headers), maxCustomHeaders)
	}
	for name, value := range headers {
		if name == "" {
			return errors.New("headers contains an empty header name")
		}
		if disallowedFetchHeaders[strings.ToLower(name)] {
			return fmt.Errorf("header %q is transport-owned and cannot be set", name)
		}
		if strings.ContainsAny(name+value, "\r\n\x00") {
			return fmt.Errorf("header %q contains CR/LF/NUL", name)
		}
		req.Header.Set(name, value)
	}
	return nil
}

// newClient builds the per-call client over the shared guarded
// transport. No client-level timeout: the inbound request context
// (the gateway's invoke/export timeout) bounds the whole call, and a
// second deadline here would cut off large streamed exports.
func (h *handler) newClient(follow bool) *http.Client {
	checkRedirect := func(_ *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}
	if !follow {
		checkRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return &http.Client{Transport: h.transport, CheckRedirect: checkRedirect}
}

// writeFetchFailure maps an outbound failure to a status the model can
// act on: 403 for a guard refusal (fix the destination), 504 for a
// timeout (narrow the request or raise timeout_seconds), 502 for
// everything else. The error text is rebuilt around the redacted URL —
// net/http error strings embed the full URL, which for a presigned
// link includes the signature.
func writeFetchFailure(w http.ResponseWriter, u *url.URL, err error) {
	var blocked *blockedDestinationError
	switch {
	case errors.As(err, &blocked):
		writeError(w, http.StatusForbidden, blocked.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, fmt.Sprintf("fetching %s timed out", redactedURL(u)))
	default:
		writeError(w, http.StatusBadGateway, fmt.Sprintf("fetching %s failed: %s", redactedURL(u), innermostErrorText(err)))
	}
}

// innermostErrorText unwraps a *url.Error to its cause so the message
// relayed to the model does not carry the full URL (query string
// included) that url.Error.Error() embeds.
func innermostErrorText(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err.Error()
	}
	return err.Error()
}

// expectedContentTypeMismatch compares the caller's declared
// expectation against the fetched Content-Type by media type
// (parameters like charset are ignored). A declared expectation that
// does not match fails the call: landing an HTML error page in an
// asset labeled as CSV would be worse than failing loudly.
func expectedContentTypeMismatch(expected, got string) string {
	if expected == "" {
		return ""
	}
	wantType, _, wErr := mime.ParseMediaType(expected)
	gotType, _, gErr := mime.ParseMediaType(got)
	if wErr == nil && gErr == nil && strings.EqualFold(wantType, gotType) {
		return ""
	}
	return fmt.Sprintf("response Content-Type %q does not match expected_content_type %q", got, expected)
}

// bodyAborter is an http.ResponseWriter that can fail its response
// body mid-stream after the header is committed. The gateway's
// internal transport implements it (its body is a pipe); a plain
// ResponseWriter does not, so the abort degrades to a logged warning.
type bodyAborter interface {
	CloseWithError(error)
}

// relayResponse streams the fetched response through: status and
// Content-Type verbatim, Content-Length when the upstream declared one
// (the gateway's export path uses it for its early over-cap check), and
// Link, which is the pagination signal a walk over a fetched document
// reads (#1544). The one slog line records host and path only — never
// the query string, which for a presigned URL is the credential.
func relayResponse(w http.ResponseWriter, resp *http.Response, u *url.URL) {
	if ct := resp.Header.Get(contentTypeHeader); ct != "" {
		w.Header().Set(contentTypeHeader, ct)
	}
	for _, link := range resp.Header.Values(linkHeader) {
		w.Header().Add(linkHeader, link)
	}
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, decimalBase))
	}
	w.WriteHeader(resp.StatusCode)
	written, copyErr := io.Copy(w, resp.Body)
	if copyErr != nil {
		// The status line is already sent, so the outcome cannot be
		// changed to an error status. Instead ABORT the response body
		// with an error: the export path streams this body to S3 and
		// its all-or-nothing contract depends on distinguishing a
		// truncated upstream fetch from a clean EOF. Without the abort
		// the reader sees a short but "complete" body and would land a
		// partial asset. bodyAborter is satisfied by the gateway's
		// internal transport; a plain ResponseWriter (unit tests) just
		// gets the logged warning.
		if ab, ok := w.(bodyAborter); ok {
			ab.CloseWithError(fmt.Errorf("upstream fetch stream interrupted after %d bytes: %w", written, copyErr))
		}
		slog.Warn("utilhandler: fetch body relay interrupted",
			"url", redactedURL(u), "written", written, "error", copyErr)
		return
	}
	slog.Info("utilhandler: fetch_url",
		"url", redactedURL(u), "status", resp.StatusCode, "bytes", written)
}

// redactedURL renders scheme://host/path, dropping query and fragment.
// Presigned-URL credentials live in the query string; they must never
// reach logs or relayed error text.
func redactedURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	r := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	return r.String()
}

// writeError emits the handler's JSON error envelope. The gateway
// relays it as the operation's response body, so the model sees a
// structured, actionable message.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set(contentTypeHeader, "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck // in-process writer
}
