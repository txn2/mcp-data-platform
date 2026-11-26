package refscheme

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// apiPrefix is where the portal's and the admin's JSON routes are served.
const apiPrefix = "/api/"

// Prefixes are the paths the HTTP boundary rewrites: the JSON API, and the
// endpoints a managed script answers (/endpoints/<name>, and an environment's
// /environments/<env>/endpoints/<name>), whose responses leave for a person
// and whose requests carry back references a response gave out (#2788): a
// form that sent a reference it was handed was refused by a script that
// validates the canonical form.
var Prefixes = []string{apiPrefix, "/endpoints/", "/environments/"}

// rewritten reports a path the boundary rewrites.
func rewritten(p string) bool {
	for _, prefix := range Prefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// maxRewrittenBody bounds a JSON body read whole to be rewritten; a larger one
// passes as it is.
const maxRewrittenBody = 32 << 20

// HTTP is the HTTP boundary of the scheme for the JSON API (#2776) and the
// endpoints (#2788): a reference under the deployment's scheme in a request's
// path, query or JSON body reaches the handler canonical, and every reference
// in a JSON answer leaves under the deployment's scheme. Any other answer (a
// file, a stream, a page) is sent as it is. With no scheme set it is the
// handler itself.
func HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Rewrites() || !rewritten(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		canonHTTPRequest(r, strings.HasPrefix(r.URL.Path, apiPrefix))
		dw := &displayWriter{ResponseWriter: w}
		defer dw.finish()
		next.ServeHTTP(dw, r)
	})
}

// canonHTTPRequest makes a request's path and query canonical, and its JSON
// body when withBody: an endpoint's body is made canonical by the endpoint
// itself (CanonJSONBody), after its binding's body limit and rate limits, so
// nothing here reads a public endpoint's body before they are applied.
func canonHTTPRequest(r *http.Request, withBody bool) {
	r.URL.Path = canonPath(r.URL.Path)
	if r.URL.RawPath != "" {
		r.URL.RawPath = canonPath(r.URL.RawPath)
	}
	r.URL.RawQuery = Canon(r.URL.RawQuery)
	if !withBody || r.Body == nil || r.Body == http.NoBody || !isJSON(r.Header.Get("Content-Type")) || r.ContentLength > maxRewrittenBody {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRewrittenBody+1))
	if err != nil || len(body) > maxRewrittenBody {
		// Too large to rewrite, or unreadable: the handler reads it as sent,
		// what was read first included, rather than a body cut at the bound.
		r.Body = readCloser{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
		return
	}
	_ = r.Body.Close()
	body = CanonBytes(body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

// readCloser reads one reader and closes another.
type readCloser struct {
	io.Reader
	io.Closer
}

// CanonJSONBody is a request body with its references canonical when its
// content type is JSON and a scheme is set, and the body as it is otherwise.
// An endpoint calls it on the body its binding's limit admitted (#2788).
func CanonJSONBody(contentType string, body []byte) []byte {
	if !Rewrites() || !isJSON(contentType) {
		return body
	}
	return CanonBytes(body)
}

// canonPath is a path with each segment that is a reference canonical: a
// route names a reference as one segment.
func canonPath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = Canon(seg)
	}
	return strings.Join(segs, "/")
}

func isJSON(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "application/problem+json")
}

// displayWriter holds a JSON answer to rewrite it, and passes any other
// through.
type displayWriter struct {
	http.ResponseWriter
	decided bool
	holding bool
	status  int
	buf     bytes.Buffer
}

func (d *displayWriter) decide(status int) {
	if d.decided {
		return
	}
	d.decided, d.status = true, status
	h := d.Header()
	if isJSON(h.Get("Content-Type")) && h.Get("Content-Encoding") == "" {
		d.holding = true
		return
	}
	d.ResponseWriter.WriteHeader(status)
}

// WriteHeader decides whether the answer is held.
func (d *displayWriter) WriteHeader(status int) { d.decide(status) }

// Write holds a JSON answer, up to the bound, and passes anything else.
func (d *displayWriter) Write(p []byte) (int, error) {
	d.decide(http.StatusOK)
	if !d.holding {
		return d.ResponseWriter.Write(p) //nolint:wrapcheck // the writer's own error
	}
	if d.buf.Len()+len(p) > maxRewrittenBody {
		d.release(false)
		return d.ResponseWriter.Write(p) //nolint:wrapcheck // the writer's own error
	}
	return d.buf.Write(p) //nolint:wrapcheck // never fails
}

// release sends what is held, rewritten when rewrite is true.
func (d *displayWriter) release(rewrite bool) {
	if !d.holding {
		return
	}
	d.holding = false
	body := d.buf.Bytes()
	if rewrite {
		body = DisplayBytes(body)
		d.Header().Set("Content-Length", strconv.Itoa(len(body)))
	} else {
		d.Header().Del("Content-Length")
	}
	d.ResponseWriter.WriteHeader(d.status)
	_, _ = d.ResponseWriter.Write(body)
	d.buf.Reset()
}

func (d *displayWriter) finish() {
	if !d.decided {
		return
	}
	d.release(true)
}

// Flush sends what is held as it is: a handler that flushes is streaming.
func (d *displayWriter) Flush() {
	d.release(true)
	if f, ok := d.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack hands over the connection of a handler that takes it.
func (d *displayWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := d.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the connection cannot be taken over")
	}
	return h.Hijack() //nolint:wrapcheck // the server's own error
}

// Unwrap is the writer under it, for http.ResponseController.
func (d *displayWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }
