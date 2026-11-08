package standalone

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"path"
	"strings"
	"sync"
)

// session is one export's fetches, their budget and what they found.
type session struct {
	// mu guards the fields below it: the JSX build reads vendor files from
	// esbuild's own goroutines.
	mu        sync.Mutex
	in        Input
	spent     int64
	fetched   int
	cache     map[string]fetched
	notices   []Notice
	noticed   map[string]bool
	dataReads []string
	dataSeen  map[string]bool
}

// fetched is one URL's outcome, kept so a URL named twice is fetched once.
type fetched struct {
	body        []byte
	contentType string
	err         error
}

func newSession(in Input) *session {
	return &session{
		in: in, cache: map[string]fetched{},
		noticed: map[string]bool{}, dataSeen: map[string]bool{},
	}
}

// Where a URL points.
type place int

const (
	placeNone     place = iota // a data: URI, a fragment, or nothing to fetch
	placeOwn                   // the reference or vendor route
	placeExternal              // another host
	placeOther                 // a platform path the file cannot carry, or a relative path
)

// resolve reads ref against base (the URL of the file naming it) and says
// where it points.
func (s *session) resolve(base *url.URL, ref string) (*url.URL, place) {
	ref = strings.TrimSpace(ref)
	if inDocument(ref) {
		return nil, placeNone
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil, placeOther
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	switch {
	case u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/"), s.isOrigin(u):
		return u, ownPlace(u)
	case u.Scheme != "http" && u.Scheme != "https":
		return u, placeNone
	default:
		return u, placeExternal
	}
}

// inDocument reports whether a reference needs nothing fetched: empty, a
// fragment, or a data: or blob: URI.
func inDocument(ref string) bool {
	lower := strings.ToLower(ref)
	return ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:")
}

// fetchedRef is one fetched reference: where it resolved and what it answered.
type fetchedRef struct {
	u     *url.URL
	where place
	fetched
}

// get resolves ref against base and fetches it. ok is false, with a notice
// where there is something to say, when there is nothing to fetch, when the
// address is one the file cannot carry, and when the fetch failed.
func (s *session) get(ctx context.Context, base *url.URL, ref string) (f fetchedRef, ok bool) {
	f.u, f.where = s.resolve(base, ref)
	switch f.where {
	case placeOwn, placeExternal:
	case placeOther:
		s.notice(ref, "a platform or relative address the file cannot carry")
		return f, false
	default:
		return f, false
	}
	f.fetched = s.fetch(ctx, f.u, f.where)
	if f.err != nil {
		s.notice(displayURL(f.u, f.where), "could not be fetched: "+errorText(f.err))
		return f, false
	}
	return f, true
}

// ownPlace is placeOwn for the two routes the export reads in-process.
func ownPlace(u *url.URL) place {
	p := path.Clean(u.Path)
	if strings.HasPrefix(p, refsPrefix) || strings.HasPrefix(p, vendorPrefix) {
		return placeOwn
	}
	return placeOther
}

func (s *session) isOrigin(u *url.URL) bool {
	o := s.in.Origin
	return o != nil && o.Host != "" && strings.EqualFold(u.Host, o.Host) && u.Scheme == o.Scheme
}

// isRef reports whether u is a declared reference's URL.
func isRef(u *url.URL) bool {
	return strings.HasPrefix(path.Clean(u.Path), strings.TrimSuffix(refsPrefix, "/"))
}

// fetch reads u within the budget, once.
func (s *session) fetch(ctx context.Context, u *url.URL, where place) fetched {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := u.String()
	if got, ok := s.cache[key]; ok {
		return got
	}
	got := s.fetchUncached(ctx, u, where)
	s.cache[key] = got
	return got
}

var (
	errBudgetFiles = errors.New("the export reached its limit on files fetched")
	errBudgetBytes = errors.New("the export reached its limit on bytes fetched")
)

func (s *session) fetchUncached(ctx context.Context, u *url.URL, where place) fetched {
	if s.fetched >= s.in.Limits.Files {
		return fetched{err: errBudgetFiles}
	}
	remaining := s.in.Limits.Total - s.spent
	if remaining <= 0 {
		return fetched{err: errBudgetBytes}
	}
	limit := min(s.in.Limits.PerFile, remaining)
	s.fetched++
	fetcher := s.in.External
	target := u
	if where == placeOwn {
		fetcher = s.in.Own
		// The platform's own routes are read by path; the origin a reader
		// reaches them at is not a host the platform dials.
		target = &url.URL{Path: path.Clean(u.Path), RawQuery: u.RawQuery}
	}
	if fetcher == nil {
		return fetched{err: errors.New("no fetcher for this address")}
	}
	fctx, cancel := context.WithTimeout(ctx, s.in.Limits.FileTimeout)
	defer cancel()
	body, ct, err := fetcher.Fetch(fctx, target, limit)
	if err != nil {
		return fetched{err: err}
	}
	s.spent += int64(len(body))
	return fetched{body: body, contentType: ct}
}

// notice records a file left as its URL.
func (s *session) notice(u, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := u + "\x00" + reason
	if s.noticed[key] {
		return
	}
	s.noticed[key] = true
	s.notices = append(s.notices, Notice{URL: u, Reason: reason})
}

// dataRead records a declared file the document reads at run time.
func (s *session) dataRead(u *url.URL, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := path.Base(u.Path)
	if s.dataSeen[name] {
		return
	}
	s.dataSeen[name] = true
	kind := baseType(contentType)
	if kind == "" {
		kind = "a file"
	}
	s.dataReads = append(s.dataReads, "it loads a "+kind+" file from the platform while it renders")
}

// dataURI is body as a data: URI of its media type.
//
// The media type comes from the answering host's Content-Type header, and the
// URI is written inside a quoted CSS url(), a script's string literal and an
// attribute. So the type written is never the header's text: it is looked up
// in dataTypes, a table of the types a document embeds, and any type not in it
// is application/octet-stream. No header can end the string it is written in.
func dataURI(contentType string, body []byte) string {
	ct, ok := dataTypes[baseType(contentType)]
	if !ok {
		ct = octetStream
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(body)
}

const (
	octetStream = "application/octet-stream"
	typeCSS     = "text/css"
)

// dataTypes are the media types a data: URI is written with, each mapped to
// itself: the value written is this table's constant, not the header's text.
var dataTypes = func() map[string]string {
	types := []string{
		"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/svg+xml",
		"image/bmp", "image/x-icon", "image/vnd.microsoft.icon", "image/apng",
		"font/woff", "font/woff2", "font/ttf", "font/otf", "font/collection",
		"application/font-woff", "application/font-woff2", "application/x-font-ttf",
		"application/x-font-otf", "application/vnd.ms-fontobject",
		typeCSS, "text/csv", "text/plain", "text/javascript", "application/javascript",
		"application/json", "application/pdf",
		"audio/mpeg", "audio/ogg", "audio/wav", "audio/webm", "audio/aac", "audio/flac",
		"video/mp4", "video/webm", "video/ogg",
		"text/vtt",
	}
	m := make(map[string]string, len(types))
	for _, t := range types {
		m[t] = t
	}
	return m
}()

// Media families.
func isCSS(ct string) bool { return baseType(ct) == typeCSS }

// embeddable is a media type a document shows rather than reads: what a data:
// URI in an element or a stylesheet carries.
func embeddable(ct string) bool {
	t := baseType(ct)
	switch {
	case strings.HasPrefix(t, "image/"), strings.HasPrefix(t, "font/"),
		strings.HasPrefix(t, "audio/"), strings.HasPrefix(t, "video/"),
		strings.HasPrefix(t, "application/font-"), strings.HasPrefix(t, "application/x-font-"),
		t == "application/vnd.ms-fontobject", t == typeCSS:
		return true
	}
	return false
}

// inlineAsset is the data: URI for a file an element or a stylesheet shows,
// or "" with a notice when it cannot be carried. A stylesheet is itself
// inlined, its own URLs resolved against where it came from.
func (s *session) inlineAsset(ctx context.Context, base *url.URL, ref string) string {
	f, ok := s.get(ctx, base, ref)
	if !ok {
		return ""
	}
	if isCSS(f.contentType) {
		return dataURI(typeCSS, []byte(s.css(ctx, f.u, string(f.body), 0)))
	}
	return dataURI(f.contentType, f.body)
}

// displayURL is how a notice names a URL: a declared reference by its file
// name only, since its path carries the capability it is served under.
func displayURL(u *url.URL, where place) string {
	if where == placeOwn && isRef(u) {
		return "a declared file on the platform"
	}
	return u.String()
}
