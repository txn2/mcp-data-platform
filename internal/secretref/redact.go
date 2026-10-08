package secretref

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"sort"
	"sync"
)

// MinValueLength is the shortest value a secret may hold. Redaction replaces
// every occurrence of a value in a response, and a value of a character or
// two would rewrite ordinary text.
const MinValueLength = 6

// Redaction is what a value is replaced with in a response.
func Redaction(name string) string { return "[REDACTED:" + name + "]" }

// Redactor replaces the values of the secrets one call used with their
// redaction. A call adds each value as it fills it; the response side then
// passes everything it returns through the Redactor.
//
// Each value is matched as written and in the forms an upstream echoes one
// in: escaped inside a JSON string (once, or twice for a JSON body echoed
// inside JSON), and query- and path-escaped.
type Redactor struct {
	mu    sync.Mutex
	pairs []pair
}

// pair is one form of one value and what replaces it.
type pair struct {
	from []byte
	to   []byte
}

// Add records a value a call sent under name.
func (r *Redactor) Add(name, value string) {
	if r == nil || value == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	to := []byte(Redaction(name))
	// JSON-escaped twice too: an upstream that echoes a JSON request body
	// inside its own JSON answer escapes the value's escapes again.
	for _, form := range []string{value, JSONString(value), JSONString(JSONString(value)), url.QueryEscape(value), url.PathEscape(value)} {
		if !r.has(form) {
			r.pairs = append(r.pairs, pair{from: []byte(form), to: to})
		}
	}
	// Longest first, so a value containing another is replaced whole.
	sort.SliceStable(r.pairs, func(i, j int) bool { return len(r.pairs[i].from) > len(r.pairs[j].from) })
}

// has reports whether form is already recorded. The caller holds mu.
func (r *Redactor) has(form string) bool {
	for _, p := range r.pairs {
		if string(p.from) == form {
			return true
		}
	}
	return false
}

// Empty reports whether no value was recorded, so a caller can skip the
// pass over a response that cannot need it.
func (r *Redactor) Empty() bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pairs) == 0
}

// snapshot is the recorded pairs.
func (r *Redactor) snapshot() []pair {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pair(nil), r.pairs...)
}

// Bytes returns b with every recorded value replaced.
func (r *Redactor) Bytes(b []byte) []byte {
	if r.Empty() {
		return b
	}
	pairs := r.snapshot()
	out, _ := redactPrefix(b, pairs, true)
	return out
}

// String returns s with every recorded value replaced.
func (r *Redactor) String(s string) string {
	if r.Empty() {
		return s
	}
	return string(r.Bytes([]byte(s)))
}

// Strings redacts each value of a header-shaped map in place.
func (r *Redactor) Strings(h map[string][]string) {
	if r.Empty() {
		return
	}
	for k, vs := range h {
		for i, v := range vs {
			h[k][i] = r.String(v)
		}
	}
}

// Reader redacts a stream, for a response written somewhere without being
// held whole. It holds back the bytes that could be the start of a value
// until the next read shows whether they are.
func (r *Redactor) Reader(src io.ReadCloser) io.ReadCloser {
	if r.Empty() {
		return src
	}
	pairs := r.snapshot()
	return &redactReader{src: src, pairs: pairs}
}

// redactPrefix redacts b. When final is false it stops where a value could
// still begin in bytes not read yet, and returns that tail unprocessed.
func redactPrefix(b []byte, pairs []pair, final bool) (out, rest []byte) {
	var buf bytes.Buffer
	for {
		at, p := firstMatch(b, pairs)
		if at < 0 {
			break
		}
		_, _ = buf.Write(b[:at])
		_, _ = buf.Write(p.to)
		b = b[at+len(p.from):]
	}
	if final {
		_, _ = buf.Write(b)
		return buf.Bytes(), nil
	}
	hold := 0
	for _, p := range pairs {
		hold = max(hold, partialSuffix(b, p.from))
	}
	_, _ = buf.Write(b[:len(b)-hold])
	return buf.Bytes(), b[len(b)-hold:]
}

// firstMatch is the earliest whole value in b, the longest at a tie.
func firstMatch(b []byte, pairs []pair) (int, pair) {
	at, found := -1, pair{}
	for _, p := range pairs {
		i := bytes.Index(b, p.from)
		if i >= 0 && (at < 0 || i < at) {
			at, found = i, p
		}
	}
	return at, found
}

// partialSuffix is the length of the longest tail of b that is a proper
// prefix of v: bytes that may be the start of v.
func partialSuffix(b, v []byte) int {
	for n := min(len(b), len(v)-1); n > 0; n-- {
		if bytes.HasPrefix(v, b[len(b)-n:]) {
			return n
		}
	}
	return 0
}

// redactReader is Redactor.Reader's stream.
type redactReader struct {
	src     io.ReadCloser
	pairs   []pair
	pending []byte
	out     []byte
	eof     bool
	buf     [32 * 1024]byte
}

// Read returns redacted bytes.
func (rr *redactReader) Read(p []byte) (int, error) {
	for len(rr.out) == 0 {
		if rr.eof {
			if len(rr.pending) == 0 {
				return 0, io.EOF
			}
			rr.out, _ = redactPrefix(rr.pending, rr.pairs, true)
			rr.pending = nil
			continue
		}
		n, err := rr.src.Read(rr.buf[:])
		rr.pending = append(rr.pending, rr.buf[:n]...)
		switch {
		case err == io.EOF:
			rr.eof = true
		case err != nil:
			return 0, err //nolint:wrapcheck // the stream's own error, passed through
		}
		rr.out, rr.pending = redactPrefix(rr.pending, rr.pairs, false)
		rr.pending = append([]byte(nil), rr.pending...)
	}
	n := copy(p, rr.out)
	rr.out = rr.out[n:]
	return n, nil
}

// Close closes the source.
func (rr *redactReader) Close() error { return rr.src.Close() } //nolint:wrapcheck // the stream's own error

// ctxKey carries a call's Redactor.
type ctxKey struct{}

// WithRedactor returns ctx carrying a fresh Redactor, and the Redactor, for
// one call: the request side adds to it, the response side reads it.
func WithRedactor(ctx context.Context) (context.Context, *Redactor) {
	r := &Redactor{}
	return context.WithValue(ctx, ctxKey{}, r), r
}

// FromContext is the call's Redactor, or nil when the call made none, which
// every method treats as empty.
func FromContext(ctx context.Context) *Redactor {
	r, _ := ctx.Value(ctxKey{}).(*Redactor)
	return r
}

// Error is err's message with every recorded value replaced.
func (r *Redactor) Error(err error) string {
	if err == nil {
		return ""
	}
	return r.String(err.Error())
}
