// Package blobserve writes stored binary content to an HTTP response.
//
// Every raw-content endpoint in the platform — portal assets, asset versions,
// thumbnails, public share content, managed resources — serves bytes that a
// user uploaded or an upstream API produced, under a content type that came
// from the same untrusted place. They all need the same four things, and
// getting any of them wrong on one endpoint is a bug on that endpoint alone,
// which is why this is one function rather than a convention:
//
//   - X-Content-Type-Options: nosniff, so a browser cannot decide for itself
//     that a text/plain response is really HTML and run it.
//   - A Content-Type reduced to a parsed, parameter-free media type, so a
//     stored value like `text/plain; charset=utf-8"><script>` cannot smuggle
//     anything into the header.
//   - Content-Disposition: attachment for active types (HTML, JSX, SVG,
//     JavaScript), which must never render inline on the platform's own origin,
//     and inline for the passive families a viewer embeds.
//   - Byte-range support, so an audio or video element can seek without
//     downloading the whole object first.
package blobserve

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Options describes one blob to serve.
type Options struct {
	// Name is the object's display filename. It is used for the
	// Content-Disposition filename and must not be empty for downloads.
	Name string

	// ContentType is the stored media type. It is sanitized before it reaches
	// the response; an empty or unparseable value serves as
	// application/octet-stream.
	ContentType string

	// ModTime is the object's last-modified time, used for If-Range and
	// conditional-request handling. The zero value disables both.
	ModTime time.Time

	// Data is the object's full content.
	Data []byte

	// ForceAttachment serves the blob as a download regardless of family. Set
	// it on endpoints that exist to download, not to preview.
	ForceAttachment bool
}

// Serve writes the blob to w, honoring range requests in r.
func Serve(w http.ResponseWriter, r *http.Request, opts Options) {
	ct := contenttype.Normalize(opts.ContentType)
	if ct == "" {
		ct = contenttype.OctetStream
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", disposition(ct, opts))

	// http.ServeContent supplies Range, If-Range, If-Modified-Since,
	// Content-Length and the 206/416 responses. It only sniffs a Content-Type
	// when the header is unset, and it is set above.
	http.ServeContent(w, r, opts.Name, opts.ModTime, bytes.NewReader(opts.Data))
}

// disposition builds the Content-Disposition header. Active types are always
// attachments: rendering author-controlled HTML, JSX, SVG or JavaScript inline
// on the platform's origin would let a stored asset script the platform.
func disposition(ct string, opts Options) string {
	kind := "inline"
	if opts.ForceAttachment || contenttype.IsActive(ct) {
		kind = "attachment"
	}
	name := sanitizeFilename(opts.Name)
	if name == "" {
		return kind
	}
	return fmt.Sprintf("%s; filename=%q", kind, name)
}

// sanitizeFilename strips the characters that would break out of the quoted
// filename parameter or inject a second header.
func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f: // control characters, including CR and LF
			return -1
		case r == '"', r == '\\', r == '/':
			return '_'
		default:
			return r
		}
	}, name)
	return strings.TrimSpace(name)
}
