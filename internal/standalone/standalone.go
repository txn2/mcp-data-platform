// Package standalone turns an HTML or JSX asset into one HTML file that opens
// from disk with the network off (#2530).
//
// A stored HTML asset names the platform's served runtimes by root-relative
// path (reveal.js under /portal/vendor/), its declared images and fonts by the
// reference route (/portal/refs/), and sometimes files on other hosts (a
// Google Fonts stylesheet, a CDN script). A JSX asset is a component the
// viewer compiles and runs against React, Recharts and lucide-react from an
// import map. Downloading the stored bytes hands the reader a file that renders
// none of that from file://. Build reads every one of those files, inlines it
// (a script as an inline script, a stylesheet as a style element, an image or
// a font as a data: URI) and, for JSX, compiles the component and bundles it
// with the libraries the platform serves under /portal/vendor/jsx/.
//
// Some assets read the platform's data while they render: a declared CSV or
// JSON fetched at run time, an embedded form, a map reading the basemap
// archive. A file written from them would open without that data, so Build
// refuses them with a *PlatformDataError naming what they read, and the caller
// offers the stored source instead.
//
// Build fetches nothing itself. It is handed two Fetchers: Own answers the
// platform's own paths in-process (the caller restricts it to the reference
// and vendor routes, which answer without a session), and External answers a
// URL on another host through the egress guard. Every fetch is bounded by
// Limits, and a file that cannot be fetched or is past a bound is left as its
// URL and reported as a Notice.
package standalone

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// The media types Build accepts.
const (
	TypeHTML = "text/html"
	TypeJSX  = "text/jsx"
)

// Fetcher reads one URL.
type Fetcher interface {
	// Fetch returns the body and media type of u, reading at most limit
	// bytes; a body past limit is ErrTooLarge.
	Fetch(ctx context.Context, u *url.URL, limit int64) (body []byte, contentType string, err error)
}

// ErrTooLarge is a Fetcher's answer for a body past the limit it was given.
var ErrTooLarge = errors.New("larger than the bound")

// Limits bound one export.
type Limits struct {
	// PerFile bounds one fetched file.
	PerFile int64
	// Total bounds every fetched file together.
	Total int64
	// Files bounds how many files are fetched.
	Files int
	// Timeout bounds the whole export; FileTimeout one fetch.
	Timeout     time.Duration
	FileTimeout time.Duration
}

// DefaultLimits are the bounds an export runs under when the caller sets none.
var DefaultLimits = Limits{
	PerFile:     16 << 20,
	Total:       64 << 20,
	Files:       200,
	Timeout:     60 * time.Second,
	FileTimeout: 15 * time.Second,
}

// Input is one export.
type Input struct {
	// Document is the asset as its content route serves it: declared
	// references already rewritten to the reference route.
	Document []byte
	// ContentType is TypeHTML or TypeJSX.
	ContentType string
	// Title names the file's document, for a JSX asset that has no head.
	Title string
	// Origin is the deployment's origin as the reader reaches it; a URL at
	// it, or a root-relative path, is the platform's own.
	Origin *url.URL
	// Own reads a platform path; External a URL on another host.
	Own      Fetcher
	External Fetcher
	// Limits bounds the export; a zero field takes DefaultLimits'.
	Limits Limits
}

// Notice is a file the export left as its URL, and why: the part of the
// document that still needs the network.
type Notice struct {
	URL    string `json:"url"`
	Reason string `json:"reason"`
}

// PlatformDataError refuses an asset that reads the platform's data while it
// renders. Reasons are sentences a reader is shown.
type PlatformDataError struct {
	Reasons []string
}

func (e *PlatformDataError) Error() string {
	return "the asset reads data from the platform: " + strings.Join(e.Reasons, "; ")
}

// ErrUnsupported is a document Build does not export.
var ErrUnsupported = errors.New("only an HTML or JSX asset is exported as a standalone file")

// Build writes the standalone file for in, and names what it could not carry.
func Build(ctx context.Context, in Input) (doc []byte, notices []Notice, err error) {
	in.Limits = in.Limits.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, in.Limits.Timeout)
	defer cancel()
	kind := baseType(in.ContentType)
	if kind != TypeHTML && kind != TypeJSX {
		return nil, nil, ErrUnsupported
	}
	if reasons := platformReads(string(in.Document), in.Origin); len(reasons) > 0 {
		return nil, nil, &PlatformDataError{Reasons: reasons}
	}
	s := newSession(in)
	if kind == TypeJSX {
		doc, err = s.jsx(ctx, string(in.Document), in.Title)
	} else {
		doc, err = s.html(ctx, in.Document)
	}
	if err != nil {
		return nil, nil, err
	}
	if len(s.dataReads) > 0 {
		return nil, nil, &PlatformDataError{Reasons: s.dataReads}
	}
	return doc, s.notices, nil
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits
	if l.PerFile > 0 {
		d.PerFile = l.PerFile
	}
	if l.Total > 0 {
		d.Total = l.Total
	}
	if l.Files > 0 {
		d.Files = l.Files
	}
	if l.Timeout > 0 {
		d.Timeout = l.Timeout
	}
	if l.FileTimeout > 0 {
		d.FileTimeout = l.FileTimeout
	}
	return d
}

// baseType is a media type without its parameters, lower case.
func baseType(contentType string) string {
	t, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(t))
}

// Path prefixes the export treats specially.
const (
	refsPrefix   = "/portal/refs/"
	vendorPrefix = "/portal/vendor/"
	mapsPrefix   = "/portal/maps/"
	formsPrefix  = "/forms/"
	formScheme   = "mcp:form:"
)

// platformReads names what a document reads from the platform that a file
// cannot carry, read from its text before anything is fetched: an embedded
// form, and the basemap archive a map reads by range.
func platformReads(doc string, origin *url.URL) []string {
	var reasons []string
	if strings.Contains(doc, formScheme) || strings.Contains(doc, originPath(origin, formsPrefix)) {
		reasons = append(reasons, "it embeds a form, which submits to the platform")
	}
	if strings.Contains(doc, mapsPrefix) {
		reasons = append(reasons, "its map tiles come from the platform's basemap archive")
	}
	return reasons
}

// originPath is the absolute URL of path at origin, or path alone with no
// origin.
func originPath(origin *url.URL, path string) string {
	if origin == nil || origin.Host == "" {
		return "\x00" + path // never matches a document by accident
	}
	return origin.Scheme + "://" + origin.Host + path
}

// errorText is an error's text for a notice, kept to one short line.
func errorText(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	const limit = 160
	if len(msg) > limit {
		msg = msg[:limit] + "..."
	}
	return msg
}
