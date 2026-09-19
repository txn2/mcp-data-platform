package headless

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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
	Request   struct {
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
		rs.serveOwn(ctx, m.SessionID, p.RequestID, u.Path)
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
func (rs *render) serveOwn(ctx context.Context, session, requestID, path string) {
	if path == "/" || path == "" {
		rs.fulfill(ctx, session, requestID, reply{http.StatusOK, map[string]string{"Content-Type": "text/html; charset=utf-8"}, rs.page.Document})
		return
	}
	if rs.page.Files != nil {
		if f, ok := rs.page.Files(path); ok {
			rs.fulfill(ctx, session, requestID, reply{http.StatusOK, map[string]string{"Content-Type": f.ContentType}, f.Body})
			return
		}
	}
	rs.fulfill(ctx, session, requestID, reply{http.StatusNotFound, map[string]string{"Content-Type": "text/plain; charset=utf-8"}, []byte("not found")})
}

// servePublic fetches a public resource through the guarded client and hands
// the page its response. Redirects are handed to the page rather than
// followed, so each hop is a new request that is paused and vetted in turn and
// a module resolves its relative imports against the address it was served
// from.
func (rs *render) servePublic(ctx context.Context, session string, p pausedRequest) {
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
		rs.fail(ctx, session, p.RequestID)
		return
	}
	defer res.Body.Close() //nolint:errcheck // read-only response
	body, err := io.ReadAll(io.LimitReader(res.Body, maxPublicBody+1))
	if err != nil || len(body) > maxPublicBody {
		rs.fail(ctx, session, p.RequestID)
		return
	}
	headers := map[string]string{}
	for _, h := range forwardedResponseHeaders {
		if v := res.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	rs.fulfill(ctx, session, p.RequestID, reply{res.StatusCode, headers, body})
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
