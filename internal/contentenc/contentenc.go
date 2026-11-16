// Package contentenc is how the platform sends a file compressed: which media
// types are worth compressing, the gzip and brotli forms of a body, whether a
// form saves enough to keep, and which forms a request's Accept-Encoding
// allows. A site's published files (internal/site) and the portal's own build
// files (internal/ui, #2598) are sent by these one set of rules, and Respond
// is the rule for a body produced per request: an endpoint's answer in api and
// page mode and a form's page (#2655).
package contentenc

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

const (
	// MinBytes is the smallest body worth compressing: below it the framing
	// costs about what the compression saves.
	MinBytes = 1024

	// brotliQuality trades compression time for size. A stored file is
	// compressed once and served many times, so it leans toward size.
	brotliQuality = 9
	// fastBrotliQuality and fastGzipLevel compress a body produced per
	// request (Respond), where the time is spent on every answer.
	fastBrotliQuality = 4
	fastGzipLevel     = 5

	// A form is kept only when it is under worthwhilePercent of the original.
	worthwhilePercent = 95
	wholePercent      = 100

	floatBits = 64
)

// Compressible reports whether a file of the media type is worth compressing.
func Compressible(contentType string) bool {
	ct, _, _ := strings.Cut(contentType, ";")
	ct = strings.ToLower(strings.TrimSpace(ct))
	return strings.HasPrefix(ct, "text/") || strings.HasSuffix(ct, "+xml") || strings.HasSuffix(ct, "+json") ||
		slices.Contains([]string{
			"application/json", "application/javascript", "application/xml", "application/wasm",
			"application/x-javascript", "image/svg+xml", "font/ttf", "font/otf",
		}, ct)
}

// Gzip is body's gzip form.
func Gzip(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := compressTo(&buf, "gzip", gzipWriter, body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Brotli is body's brotli form.
func Brotli(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := compressTo(&buf, "brotli", brotliWriter, body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipWriter(dst io.Writer) (io.WriteCloser, error) {
	return gzip.NewWriterLevel(dst, gzip.BestCompression) //nolint:wrapcheck // compressTo names the form
}

func brotliWriter(dst io.Writer) (io.WriteCloser, error) {
	return brotli.NewWriterLevel(dst, brotliQuality), nil
}

// compressTo writes body's compressed form to dst, the error naming the form.
func compressTo(dst io.Writer, form string, open func(io.Writer) (io.WriteCloser, error), body []byte) error {
	w, err := open(dst)
	if err != nil {
		return fmt.Errorf("writing the %s form: %w", form, err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("writing the %s form: %w", form, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("writing the %s form: %w", form, err)
	}
	return nil
}

// Encoding names the forms Respond sends.
const (
	EncodingBrotli = "br"
	EncodingGzip   = "gzip"
)

// Respond is body as an answer produced for r is sent: brotli or gzip when r
// accepts one, the content type compresses, the body is at least MinBytes
// and the form saves enough, with Content-Encoding and Vary set on h; body
// as it is otherwise (#2655). secret says the answer may hold one of the
// platform's tokens beside text the requester chose, which is never
// compressed: its compressed length would tell an observer how well a guess
// matched the token (BREACH). A HEAD, a 204 or 304, and a body the handler
// encoded itself are sent as they are.
func Respond(r *http.Request, h http.Header, status int, body []byte, secret bool) []byte {
	if !candidate(r, h, status, body, secret) {
		return body
	}
	h.Add("Vary", "Accept-Encoding")
	out, form := encodeFor(r.Header.Get("Accept-Encoding"), body)
	if form == "" {
		return body
	}
	h.Set("Content-Encoding", form)
	return out
}

// candidate reports whether an answer may be sent compressed at all, whatever
// the request accepts.
func candidate(r *http.Request, h http.Header, status int, body []byte, secret bool) bool {
	switch {
	case secret, r.Method == http.MethodHead, status == http.StatusNoContent, status == http.StatusNotModified:
		return false
	case h.Get("Content-Encoding") != "", len(body) < MinBytes:
		return false
	}
	return Compressible(h.Get("Content-Type"))
}

// encodeFor is body in the form acceptEncoding takes first, brotli before
// gzip, and its name; "" when none is taken or none saves enough.
func encodeFor(acceptEncoding string, body []byte) (out []byte, form string) {
	br, gz := Accepted(acceptEncoding)
	var err error
	switch {
	case br:
		form = EncodingBrotli
		out, err = compressed(body, form, func(dst io.Writer) (io.WriteCloser, error) {
			return brotli.NewWriterLevel(dst, fastBrotliQuality), nil
		})
	case gz:
		form = EncodingGzip
		out, err = compressed(body, form, func(dst io.Writer) (io.WriteCloser, error) {
			return gzip.NewWriterLevel(dst, fastGzipLevel) //nolint:wrapcheck // compressTo names the form
		})
	default:
		return nil, ""
	}
	if err != nil || !Worthwhile(len(body), len(out)) {
		return nil, ""
	}
	return out, form
}

func compressed(body []byte, form string, open func(io.Writer) (io.WriteCloser, error)) ([]byte, error) {
	var buf bytes.Buffer
	if err := compressTo(&buf, form, open, body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Worthwhile reports whether a compressed form of compressed bytes saves
// enough over an original of original bytes to be kept and sent.
func Worthwhile(original, compressed int) bool {
	return compressed*wholePercent < original*worthwhilePercent
}

// Accepted reads which compressed forms an Accept-Encoding allows. An entry
// whose q is 0 refuses that form.
func Accepted(acceptEncoding string) (br, gz bool) {
	for part := range strings.SplitSeq(acceptEncoding, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if zeroWeight(params) {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "br":
			br = true
		case "gzip":
			gz = true
		}
	}
	return br, gz
}

// zeroWeight reports an Accept-Encoding entry whose q parameter refuses it.
func zeroWeight(params string) bool {
	q, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(params)), "q=")
	if !ok {
		return false
	}
	v, err := strconv.ParseFloat(q, floatBits)
	return err == nil && v == 0
}
