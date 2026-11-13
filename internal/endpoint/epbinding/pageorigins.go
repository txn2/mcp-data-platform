package epbinding

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// maxPageOrigins bounds each list of origins a page's policy names.
const maxPageOrigins = 20

// The shapes of what a page's policy names: each is written into a response
// header, so nothing but this shape reaches it. A page origin is https with
// no path; it may start with one wildcard label, *., when at least two labels
// follow it (#2554): https://*.js.stripe.com, never https://* or https://*.com.
var (
	pageCDN    = regexp.MustCompile(`^https://(\*\.)?([a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?)(:\d{1,5})?$`)
	pageOrigin = regexp.MustCompile(`^https?://[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:\d{1,5})?$`)
)

// pageList is one list of origins a page's policy names: its field, the
// directive it adds to, and what the directive allows besides the list.
type pageList struct {
	field     string
	directive string
	origins   []string
}

// pageLists are the binding's page origin lists, in the order they are
// checked: page_cdns, then the frame, connect and form lists (#2554).
func (b Binding) pageLists() []pageList {
	return []pageList{
		{field: "page_cdns", origins: b.PageCDNs},
		{field: "page_frame_origins", directive: "frame-src", origins: b.PageFrameOrigins},
		{field: "page_connect_origins", directive: "connect-src", origins: b.PageConnectOrigins},
		{field: "page_form_origins", directive: "form-action", origins: b.PageFormOrigins},
	}
}

// applyPageOrigins sets on b the page lists req names; a nil list keeps what
// the binding had.
func applyPageOrigins(b *Binding, req Request) {
	for _, set := range []struct {
		dst *[]string
		src []string
	}{
		{&b.PageFrameOrigins, req.PageFrameOrigins},
		{&b.PageConnectOrigins, req.PageConnectOrigins},
		{&b.PageFormOrigins, req.PageFormOrigins},
	} {
		if set.src != nil {
			*set.dst = set.src
		}
	}
}

// validatePage checks what a page's content policy is built from.
func (b Binding) validatePage() error {
	if b.ResponseMode != "" && !slices.Contains(ResponseModes, b.ResponseMode) {
		return fmt.Errorf("response_mode %q: one of %v", b.ResponseMode, ResponseModes)
	}
	if b.ResponseMode != ResponsePage {
		for _, l := range b.pageLists() {
			if len(l.origins) > 0 {
				return fmt.Errorf("the field %s applies to an endpoint with response_mode page", l.field)
			}
		}
		return nil
	}
	if !slices.Contains(b.Methods, http.MethodGet) {
		return errors.New("a page endpoint answers GET")
	}
	return b.validatePageOrigins()
}

// validatePageOrigins checks the origins a page's policy names.
func (b Binding) validatePageOrigins() error {
	for _, l := range b.pageLists() {
		if len(l.origins) > maxPageOrigins {
			return fmt.Errorf("too many origins in %s: at most %d", l.field, maxPageOrigins)
		}
		for _, o := range l.origins {
			if !pageOriginShape(o) {
				return fmt.Errorf("%s %q: an https origin such as https://pay.example.com, or one leading *. with at least "+
					"two labels after it (https://*.js.stripe.com), and no path", l.field, o)
			}
		}
	}
	for _, origin := range b.AllowedOrigins {
		if !pageOrigin.MatchString(origin) {
			return fmt.Errorf("allowed_origins %q: an origin, such as https://www.example.com, with no path or wildcard", origin)
		}
	}
	return nil
}

// pageOriginShape reports whether o is an origin a page's policy may name.
func pageOriginShape(o string) bool {
	m := pageCDN.FindStringSubmatch(o)
	if m == nil {
		return false
	}
	return m[1] == "" || strings.Contains(m[2], ".")
}

// PagePolicy is the content policy a page answer is sent under: its own
// origin and listed CDNs, inline script only under this response's nonce,
// framing only by the binding's allowed origins, and the sites it may frame,
// call and post to besides its own (#2554). Every value in it passed
// validatePage, and nonce is the base64 the serving layer generated. A
// binding that lists no frame, connect or form origin answers exactly the
// policy it did before they existed.
func (b Binding) PagePolicy(nonce string) string {
	cdns := strings.Join(b.PageCDNs, sourceSep)
	src := func(extra string) string {
		return strings.Join(strings.Fields("'self' "+extra+" "+cdns), sourceSep)
	}
	ancestors := "'none'"
	if len(b.AllowedOrigins) > 0 {
		ancestors = strings.Join(b.AllowedOrigins, sourceSep)
	}
	frames := ""
	if len(b.PageFrameOrigins) > 0 {
		frames = "; frame-src " + strings.Join(b.PageFrameOrigins, sourceSep)
	}
	return "default-src 'none'; script-src " + src("'nonce-"+nonce+"'") + "; style-src " + src("'unsafe-inline'") +
		"; img-src " + src("data:") + "; font-src " + src("") + frames + withOrigins(connectSelf, b.PageConnectOrigins) +
		withOrigins(formSelf, b.PageFormOrigins) + "; base-uri 'none'; frame-ancestors " + ancestors
}

// The directives a page answer always sends with its own origin, which the
// connect and form lists add to.
const (
	connectSelf = "; connect-src 'self'"
	formSelf    = "; form-action 'self'"
)

// withOrigins is base followed by the listed origins.
func withOrigins(base string, origins []string) string {
	if len(origins) == 0 {
		return base
	}
	return base + sourceSep + strings.Join(origins, sourceSep)
}

// sourceSep separates the sources of one policy directive.
const sourceSep = " "
