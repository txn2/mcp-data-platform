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

// maxRewrittenBody bounds a JSON body read whole to be rewritten; a larger one
// passes as it is.
const maxRewrittenBody = 32 << 20

// HTTP is the HTTP boundary of the scheme for the JSON API (#2776): a
// reference under the deployment's scheme in a request's path, query or JSON
// body reaches the handler canonical, and every reference in a JSON answer
// leaves under the deployment's scheme. Any other answer (a file, a stream) is
// sent as it is. With no scheme set it is the handler itself.
func HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Rewrites() || !strings.HasPrefix(r.URL.Path, apiPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		canonHTTPRequest(r)
		dw := &displayWriter{ResponseWriter: w}
		defer dw.finish()
		next.ServeHTTP(dw, r)
	})
}

func canonHTTPRequest(r *http.Request) {
	r.URL.Path = canonPath(r.URL.Path)
	if r.URL.RawPath != "" {
		r.URL.RawPath = canonPath(r.URL.RawPath)
	}
	r.URL.RawQuery = Canon(r.URL.RawQuery)
	if r.Body == nil || r.Body == http.NoBody || !isJSON(r.Header.Get("Content-Type")) || r.ContentLength > maxRewrittenBody {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRewrittenBody+1))
	_ = r.Body.Close()
	if err == nil && len(body) <= maxRewrittenBody {
		body = CanonBytes(body)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
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
