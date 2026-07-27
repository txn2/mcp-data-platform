// Package blobserve writes stored binary content to an HTTP response.
//
// Every raw-content endpoint in the platform — portal assets, asset versions,
// thumbnails, public share content, managed resources — serves bytes that a
// user uploaded or an upstream API produced, under a content type that came
// from the same untrusted place. They all need the same five things, and
// getting any of them wrong on one endpoint is a bug on that endpoint alone,
// which is why this is one function rather than a convention:
//
//   - A sandbox Content-Security-Policy, so bytes that reach a browser as a
//     document cannot script the platform's origin whatever their type.
//   - X-Content-Type-Options: nosniff, so a browser cannot decide for itself
//     that a text/plain response is really HTML and run it.
//   - A Content-Type reduced to a parsed, parameter-free media type, so a
//     stored value like `text/plain; charset=utf-8"><script>` cannot smuggle
//     anything into the header.
//   - Content-Disposition: attachment for the scriptable document families
//     (HTML, XHTML, JSX, SVG, JavaScript, XML), which must never render inline
//     on the platform's own origin, and inline for the passive families a
//     viewer embeds.
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

// sandboxCSP is the Content-Security-Policy every response carries.
//
// `default-src 'none'` denies the document every fetch it could make, and
// because script-src falls back to it, that includes inline script, event
// handler attributes and javascript: URLs. `sandbox` with no allow- tokens then
// puts the document in an opaque origin with scripting disabled outright, so a
// type this package failed to classify as scriptable still cannot read the
// platform's cookies or issue same-origin requests as the viewer.
//
// It is unconditional rather than applied only to the types classified as
// scriptable. Stored content has no legitimate need for same-origin script on
// this origin, and a conditional header would put the guarantee back on the
// classification staying complete, which is the failure this header exists to
// remove. A response the browser never turns into a document is unaffected: an
// <img>, <audio> or <video> source is a subresource, and CSP is enforced on
// documents.
//
// The two cases where it could have cost something were checked in Chrome
// against this exact policy string. A PDF still renders in the built-in viewer
// through <object> — the iframe sandbox attribute blocks plugin instantiation,
// this header does not. Attachments still download, whether reached by an
// anchor with a download attribute or by plain navigation. Script in a
// text/html and in an application/xhtml+xml document does not run.
const sandboxCSP = "default-src 'none'; sandbox"

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

	w.Header().Set("Content-Security-Policy", sandboxCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", disposition(ct, opts))

	// http.ServeContent supplies Range, If-Range, If-Modified-Since,
	// Content-Length and the 206/416 responses. It only sniffs a Content-Type
	// when the header is unset, and it is set above.
	http.ServeContent(w, r, opts.Name, opts.ModTime, bytes.NewReader(opts.Data))
}

// disposition builds the Content-Disposition header. Scriptable document types
// are always attachments: rendering author-controlled HTML, XHTML, JSX, SVG,
// JavaScript or XML inline on the platform's origin would let a stored asset
// script the platform.
func disposition(ct string, opts Options) string {
	kind := "inline"
	if opts.ForceAttachment || contenttype.IsScriptableDocument(ct) {
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
