package headless

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
)

// forwardedRequestHeaders are the headers of a paused request passed on to a
// public server. A public module CDN chooses what to serve by the browser's
// Accept and User-Agent, and a CORS server reflects Origin; nothing a browser
// would hold for a user -- cookies, credentials -- exists in a fresh context,
// and none is forwarded.
var forwardedRequestHeaders = []string{"Accept", "Accept-Language", "User-Agent", "Origin", "Range"}

// forwardedResponseHeaders are the headers of a public response the page is
// given. Content-Type and Location make the response what it is; the CORS and
// resource-policy headers decide whether a cross-origin module script or font
// may be used at all. Content-Encoding is not among them: the client
// decompresses what it asked to be compressed.
var forwardedResponseHeaders = []string{
	"Content-Type", "Location",
	"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers",
	"Access-Control-Allow-Methods", "Access-Control-Expose-Headers",
	"Cross-Origin-Resource-Policy", "Timing-Allow-Origin",
}

// pausedRequest is the part of a Fetch.requestPaused event a decision needs.
type pausedRequest struct {
	RequestID string `json:"requestId"`
	// ResourceType is what the browser is loading the URL as: Document,
	// Stylesheet, Script, Font, Image and so on.
	ResourceType string `json:"resourceType"`
	Request      struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	} `json:"request"`
}

// answer resolves one paused request: a file of the page's own origin, a
// public resource fetched through the guard, or a refusal. Nothing is ever
// continued to the browser's own network.
func (rs *render) answer(m message) {
	var p pausedRequest
	if json.Unmarshal(m.Params, &p) != nil || p.RequestID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), publicFetchTimeout+disposeTimeout)
	defer cancel()

	u, err := url.Parse(p.Request.URL)
	switch {
	case err != nil:
		rs.refuse(ctx, m.SessionID, p.RequestID)
	case strings.EqualFold(u.Host, rs.host):
		rs.serveOwn(ctx, m.SessionID, p.RequestID, u.Path, requestHeader(p.Request.Headers))
	case rs.r.public != nil && fetchable(u, p.Request.Method):
		rs.servePublic(ctx, m.SessionID, p)
	default:
		rs.refuse(ctx, m.SessionID, p.RequestID)
	}
}

// fetchable reports whether a request is one the platform may make on the
// page's behalf: a read of an http or https URL.
func fetchable(u *url.URL, method string) bool {
	web := u.Scheme == "http" || u.Scheme == schemeHTTPS
	return web && (method == http.MethodGet || method == http.MethodHead)
}

// serveOwn answers a request on the page's own origin: the document at the
// root, a file the page declared, or a 404.
func (rs *render) serveOwn(ctx context.Context, session, requestID, path string, header http.Header) {
	if path == "/" || path == "" {
		rs.fulfill(ctx, session, requestID, reply{http.StatusOK, map[string]string{"Content-Type": "text/html; charset=utf-8"}, rs.page.Document})
		return
	}
	if rs.page.Files != nil {
		if f, ok := rs.page.Files(path, header); ok {
			if f.Status >= http.StatusInternalServerError {
				rs.noteOwnFailure(path, f.Status)
			}
			rs.fulfill(ctx, session, requestID, ownReply(f))
			return
		}
	}
	rs.fulfill(ctx, session, requestID, reply{http.StatusNotFound, map[string]string{"Content-Type": "text/plain; charset=utf-8"}, []byte("not found")})
}

// requestHeader is a paused request's headers as an http.Header.
func requestHeader(h map[string]string) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		out.Set(k, v)
	}
	return out
}

// routeResponseHeaders are the headers of a platform route's answer a page is
// given beside its type: what a ranged read is checked by, and what a read
// from a sandboxed frame, whose origin is opaque, needs to be allowed at all.
var routeResponseHeaders = []string{
	"Content-Range", "Accept-Ranges", "ETag", "Cache-Control",
	"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Expose-Headers",
	"Access-Control-Allow-Headers", "Access-Control-Allow-Methods",
}

// RouteFile is a platform route's in-process answer as a file of the page's
// own origin: its status, body and type, and the headers a page reads it by.
func RouteFile(status int, header http.Header, body []byte) File {
	f := File{Body: body, ContentType: header.Get("Content-Type"), Status: status, Header: map[string]string{}}
	for _, h := range routeResponseHeaders {
		if v := header.Get(h); v != "" {
			f.Header[h] = v
		}
	}
	return f
}

// ownReply is the response to a file of the page's own origin.
func ownReply(f File) reply {
	status := f.Status
	if status == 0 {
		status = http.StatusOK
	}
	headers := make(map[string]string, len(f.Header))
	maps.Copy(headers, f.Header)
	headers["Content-Type"] = f.ContentType
	return reply{status, headers, f.Body}
}

// servePublic fetches a public resource through the guarded client and hands
// the page its response. Redirects are handed to the page rather than
// followed, so each hop is a new request that is paused and vetted in turn and
// a module resolves its relative imports against the address it was served
// from.
func (rs *render) servePublic(ctx context.Context, session string, p pausedRequest) {
	if r, ok := rs.r.cache.get(p); ok {
		rs.fulfill(ctx, session, p.RequestID, r)
		return
	}
	fctx, cancel := context.WithTimeout(ctx, publicFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, p.Request.Method, p.Request.URL, http.NoBody)
	if err != nil {
		rs.refuse(ctx, session, p.RequestID)
		return
	}
	for _, h := range forwardedRequestHeaders {
		if v := headerValue(p.Request.Headers, h); v != "" {
			req.Header.Set(h, v)
		}
	}
	res, err := rs.r.public.Do(req) // #nosec G107 G704 -- every dial passes the egress guard the caller built this client with
	if err != nil {
		var blocked *egressguard.BlockedError
		if errors.As(err, &blocked) {
			rs.refuse(ctx, session, p.RequestID)
			return
		}
		rs.noteMissing(p, "it could not be fetched")
		rs.fail(ctx, session, p.RequestID)
		return
	}
	defer res.Body.Close() //nolint:errcheck // read-only response
	body, err := io.ReadAll(io.LimitReader(res.Body, maxPublicBody+1))
	if err != nil || len(body) > maxPublicBody {
		rs.noteMissing(p, "it could not be read")
		rs.fail(ctx, session, p.RequestID)
		return
	}
	if transient(res.StatusCode) {
		rs.noteMissing(p, "its server answered "+strconv.Itoa(res.StatusCode))
	}
	headers := map[string]string{}
	for _, h := range forwardedResponseHeaders {
		if v := res.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	r := reply{res.StatusCode, headers, body}
	rs.r.cache.put(p, r, res.Header.Get("Cache-Control"))
	rs.fulfill(ctx, session, p.RequestID, r)
}

// transient reports whether a public server's status says it may answer if
// asked again: it was overloaded or failing, not telling the document the file
// does not exist.
func transient(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

// neededTypes are the loads a document cannot be drawn as written without: a
// page whose stylesheet, script or module did not arrive draws unstyled or
// not at all (#2122). A font is not among them, since a page in its fallback
// face is still the page (#2062), nor an image, which the document's own
// failure handling draws around.
var neededTypes = map[string]bool{"Stylesheet": true, "Script": true}

// noteMissing records a public file the document needed and did not get,
// for a page that asked to fail on one.
func (rs *render) noteMissing(p pausedRequest, why string) {
	if !rs.page.RequireSubresources || !neededTypes[p.ResourceType] {
		return
	}
	rs.mu.Lock()
	rs.missing = append(rs.missing, strings.ToLower(p.ResourceType)+" "+p.Request.URL+" ("+why+")")
	rs.mu.Unlock()
}

// noteOwnFailure records a file the page declared that the platform could not
// serve, for a page that asked to fail on one: a font, an image or a script
// the document references drawn without because storage was slow is not the
// document as it stands, whatever its type (#2218).
func (rs *render) noteOwnFailure(path string, status int) {
	if !rs.page.RequireSubresources {
		return
	}
	rs.mu.Lock()
	rs.missing = append(rs.missing, fmt.Sprintf("%s (the platform answered %d)", path, status))
	rs.mu.Unlock()
}

// missingFiles is the error a render whose page needed a public file it did
// not get ends with, wrapping ErrIncomplete, or nil.
func (rs *render) missingFiles() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.missing) == 0 {
		return nil
	}
	return fmt.Errorf("headless: %d file(s) the document needs could not be fetched: %s: %w",
		len(rs.missing), strings.Join(rs.missing, "; "), ErrIncomplete)
}

// headerValue reads a header from the case-preserving map the browser sends.
func headerValue(headers map[string]string, name string) string {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// reply is the response a paused request is fulfilled with.
type reply struct {
	status  int
	headers map[string]string
	body    []byte
}

func (rs *render) fulfill(ctx context.Context, session, requestID string, r reply) {
	entries := make([]map[string]string, 0, len(r.headers))
	for k, v := range r.headers {
		entries = append(entries, map[string]string{"name": k, "value": v})
	}
	_ = rs.c.call(ctx, session, "Fetch.fulfillRequest", map[string]any{ //nolint:errcheck // a request whose page is gone has nobody to answer
		"requestId": requestID, "responseCode": r.status, "responseHeaders": entries,
		"body": base64.StdEncoding.EncodeToString(r.body),
	}, nil)
}

// refuse fails a request the page is not allowed to make.
func (rs *render) refuse(ctx context.Context, session, requestID string) {
	_ = rs.c.call(ctx, session, "Fetch.failRequest", map[string]any{"requestId": requestID, "errorReason": "BlockedByClient"}, nil) //nolint:errcheck // see fulfill
}

// fail fails a request the platform tried and could not complete.
func (rs *render) fail(ctx context.Context, session, requestID string) {
	_ = rs.c.call(ctx, session, "Fetch.failRequest", map[string]any{"requestId": requestID, "errorReason": "Failed"}, nil) //nolint:errcheck // see fulfill
}
