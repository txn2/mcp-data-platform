// Package caldav reads calendars over CalDAV (RFC 4791) for the caldav
// connection kind (#2246): it finds the account's calendars, asks the server
// for the calendar objects with an occurrence in a time range, and fetches one
// object. It only reads. The requests it can send are PROPFIND and REPORT, and
// GET; there is no PUT, DELETE, MKCALENDAR or PROPPATCH here to call.
//
// It knows nothing about connections, personas or MCP. It takes the HTTP
// client a connection built (the outbound chain, with its authenticator) and
// the account's address, and speaks the protocol; reading what a calendar
// object says is event.go's.
package caldav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseBytes bounds one response body. A calendar-query over a busy
// calendar for a long range is the largest answer; anything past this is
// refused rather than read, and the caller narrows the range.
const maxResponseBytes = 16 << 20

// slash separates path segments.
const slash = "/"

// ErrTooLarge is the refusal of a response past maxResponseBytes.
var ErrTooLarge = errors.New("caldav: the server's answer is larger than 16 MiB; narrow the time range")

// Doer sends one request. *http.Client is one; the authenticator is applied
// by Authorize before Do.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client reads one CalDAV account.
type Client struct {
	// base is the address the connection names: the server root, the
	// account's principal, or its calendar home.
	base *url.URL
	doer Doer
	// authorize sets the request's credentials; nil sends none.
	authorize func(*http.Request) error
}

// New returns a Client for the account at base.
func New(base string, doer Doer, authorize func(*http.Request) error) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("caldav: base url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("caldav: base url %q must be http or https", base)
	}
	if !strings.HasSuffix(u.Path, slash) {
		u.Path += slash
	}
	return &Client{base: u, doer: doer, authorize: authorize}, nil
}

// StatusError is a non-success answer from the server.
type StatusError struct {
	Method string
	Href   string
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("caldav: %s %s answered %d %s", e.Method, e.Href, e.Status, http.StatusText(e.Status))
}

// resolve turns an href the server returned, or one a caller passed, into an
// absolute URL on the account's origin. An href on another origin is refused:
// the credentials are the account's, and a multistatus naming another host
// does not get them sent there.
func (c *Client) resolve(href string) (*url.URL, error) {
	ref, err := url.Parse(href)
	if err != nil {
		return nil, fmt.Errorf("caldav: href %q: %w", href, err)
	}
	u := c.base.ResolveReference(ref)
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host {
		return nil, fmt.Errorf("caldav: href %q is not on the account's server", href)
	}
	return u, nil
}

// davRequest is one request to the account: its method, target, Depth
// header and body, and the one status that answers it.
type davRequest struct {
	method, href, depth string
	body                []byte
	accept              int
}

// do sends r and returns the response body, failing on any status other than
// the one r accepts.
func (c *Client) do(ctx context.Context, r davRequest) ([]byte, error) {
	method := r.method
	req, err := c.request(ctx, method, r.href, r.depth, r.body)
	if err != nil {
		return nil, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("caldav: %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read to the end below
	if resp.StatusCode != r.accept {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, discardBytes))
		return nil, &StatusError{Method: method, Href: req.URL.Path, Status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("caldav: reading %s %s: %w", method, req.URL.Path, err)
	}
	if len(data) > maxResponseBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}

// discardBytes is how much of a refused answer is read before the
// connection is reused.
const discardBytes = 64 << 10

// request builds one authorized request on the account's origin.
func (c *Client) request(ctx context.Context, method, href, depth string, body []byte) (*http.Request, error) {
	u, err := c.resolve(href)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("caldav: building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	}
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	if c.authorize != nil {
		if err := c.authorize(req); err != nil {
			return nil, fmt.Errorf("caldav: authorizing request: %w", err)
		}
	}
	return req, nil
}

// propfind sends a PROPFIND for props at href with the given depth.
func (c *Client) propfind(ctx context.Context, href, depth string, props ...string) ([]response, error) {
	data, err := c.do(ctx, davRequest{method: "PROPFIND", href: href, depth: depth, body: propfindBody(props...), accept: http.StatusMultiStatus})
	if err != nil {
		return nil, err
	}
	return parseMultistatus(data)
}

// Calendar is one calendar collection on the account.
type Calendar struct {
	Href        string `json:"href"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	// Components are the component types the calendar holds (VEVENT, VTODO).
	Components []string `json:"components"`
	// CTag changes whenever anything in the calendar does; empty when the
	// server does not report one.
	CTag string `json:"ctag,omitempty"`
}

// HomeSet finds the account's calendar home: the collection its calendars are
// in. It reads current-user-principal from the base address, then the
// principal's calendar-home-set. A base address that is already the home (it
// has calendars under it and no principal) is the home.
func (c *Client) HomeSet(ctx context.Context) (string, error) {
	principal, err := c.principal(ctx)
	if err != nil {
		return "", err
	}
	if principal == "" {
		return c.base.Path, nil
	}
	rs, err := c.propfind(ctx, principal, "0", "C:calendar-home-set")
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		if h := r.prop("calendar-home-set").href(); h != "" {
			return h, nil
		}
	}
	return "", fmt.Errorf("caldav: the principal %s names no calendar home", principal)
}

// principal reads current-user-principal from the base address, and from the
// server's /.well-known/caldav when the base is the bare server root and says
// nothing. Empty means the base names no principal.
func (c *Client) principal(ctx context.Context) (string, error) {
	rs, err := c.propfind(ctx, c.base.Path, "0", "D:current-user-principal")
	if err == nil {
		for _, r := range rs {
			if p := r.prop("current-user-principal").href(); p != "" {
				return p, nil
			}
		}
		return "", nil
	}
	if c.base.Path != slash {
		return "", err
	}
	rs, wkErr := c.propfind(ctx, "/.well-known/caldav", "0", "D:current-user-principal")
	if wkErr != nil {
		return "", err
	}
	for _, r := range rs {
		if p := r.prop("current-user-principal").href(); p != "" {
			return p, nil
		}
	}
	return "", nil
}

// Calendars lists the calendar collections in the home.
func (c *Client) Calendars(ctx context.Context, home string) ([]Calendar, error) {
	rs, err := c.propfind(ctx, home, "1",
		"D:resourcetype", "D:displayname", "C:calendar-description",
		"C:supported-calendar-component-set", "CS:getctag", "A:calendar-color")
	if err != nil {
		return nil, err
	}
	cals := make([]Calendar, 0, len(rs))
	for _, r := range rs {
		if !r.prop("resourcetype").has("calendar") {
			continue
		}
		cal := Calendar{
			Href:        r.Href,
			DisplayName: r.prop("displayname").Text,
			Description: r.prop("calendar-description").Text,
			Color:       r.prop("calendar-color").Text,
			CTag:        r.prop("getctag").Text,
			Components:  r.prop("supported-calendar-component-set").attrs("comp", "name"),
		}
		if len(cal.Components) == 0 {
			// RFC 4791 5.2.3: absent means every component type.
			cal.Components = []string{"VEVENT", "VTODO", "VJOURNAL"}
		}
		if cal.DisplayName == "" {
			cal.DisplayName = lastSegment(r.Href)
		}
		cals = append(cals, cal)
	}
	return cals, nil
}

// Object is one calendar object resource: an .ics file in a calendar.
type Object struct {
	Href string `json:"href"`
	ETag string `json:"etag,omitempty"`
	Data string `json:"-"`
}

// Query asks the calendar for the event objects with an occurrence that
// overlaps [start, end) (calendar-query with a time-range filter). The server
// decides the overlap, recurrence included; the objects come back whole, every
// instance of a recurring event in one object, for event.go to expand.
func (c *Client) Query(ctx context.Context, calendar string, start, end time.Time) ([]Object, error) {
	data, err := c.do(ctx, davRequest{method: "REPORT", href: calendar, depth: "1", body: calendarQueryBody(start, end), accept: http.StatusMultiStatus})
	if err != nil {
		return nil, err
	}
	rs, err := parseMultistatus(data)
	if err != nil {
		return nil, err
	}
	return objects(rs), nil
}

// Get reads one calendar object.
func (c *Client) Get(ctx context.Context, href string) (Object, error) {
	data, err := c.do(ctx, davRequest{method: http.MethodGet, href: href, accept: http.StatusOK})
	if err != nil {
		return Object{}, err
	}
	return Object{Href: href, Data: string(data)}, nil
}

func objects(rs []response) []Object {
	out := make([]Object, 0, len(rs))
	for _, r := range rs {
		d := r.prop("calendar-data").Text
		if d == "" {
			continue
		}
		out = append(out, Object{Href: r.Href, ETag: r.prop("getetag").Text, Data: d})
	}
	return out
}

func lastSegment(href string) string {
	s := strings.TrimSuffix(href, slash)
	if i := strings.LastIndex(s, slash); i >= 0 {
		s = s[i+1:]
	}
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

func propfindBody(props ...string) []byte {
	var b bytes.Buffer
	_, _ = b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` +
		`<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" ` +
		`xmlns:CS="http://calendarserver.org/ns/" xmlns:A="http://apple.com/ns/ical/"><D:prop>`)
	for _, p := range props {
		_, _ = b.WriteString("<" + p + "/>")
	}
	_, _ = b.WriteString(`</D:prop></D:propfind>`)
	return b.Bytes()
}

// icalUTC is the form a time-range filter's bounds are written in.
const icalUTC = "20060102T150405Z"

func calendarQueryBody(start, end time.Time) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>` +
		`<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">` +
		`<D:prop><D:getetag/><C:calendar-data/></D:prop>` +
		`<C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT">` +
		`<C:time-range start="` + start.UTC().Format(icalUTC) + `" end="` + end.UTC().Format(icalUTC) + `"/>` +
		`</C:comp-filter></C:comp-filter></C:filter></C:calendar-query>`)
}

// response is one <D:response> of a multistatus, with the properties its 200
// propstat carried.
type response struct {
	Href  string
	props map[string]element
}

func (r response) prop(local string) element { return r.props[local] }

// element is a parsed XML element, enough of one to read DAV properties.
type element struct {
	Space    string
	Local    string
	Attrs    map[string]string
	Text     string
	Children []element
}

// href is the text of the element's first <D:href> child.
func (e element) href() string {
	for _, c := range e.Children {
		if c.Local == "href" {
			return strings.TrimSpace(c.Text)
		}
	}
	return ""
}

// has reports whether the element has a child with the local name.
func (e element) has(local string) bool {
	for _, c := range e.Children {
		if c.Local == local {
			return true
		}
	}
	return false
}

// attrs returns attribute attr of every child named local.
func (e element) attrs(local, attr string) []string {
	var out []string
	for _, c := range e.Children {
		if c.Local == local && c.Attrs[attr] != "" {
			out = append(out, c.Attrs[attr])
		}
	}
	return out
}

// maxElementDepth bounds how deep parseElement descends; a multistatus is a
// handful of levels deep.
const maxElementDepth = 32

// parseMultistatus reads a 207 body into its responses, keeping the
// properties of each response's 200 propstat.
func parseMultistatus(data []byte) ([]response, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	root, err := decodeRoot(dec)
	if err != nil {
		return nil, fmt.Errorf("caldav: reading multistatus: %w", err)
	}
	if root.Local != "multistatus" {
		return nil, fmt.Errorf("caldav: expected a multistatus, got <%s>", root.Local)
	}
	var out []response
	for _, r := range root.Children {
		if r.Local != "response" {
			continue
		}
		resp := response{props: map[string]element{}}
		for _, c := range r.Children {
			switch c.Local {
			case "href":
				resp.Href = strings.TrimSpace(c.Text)
			case "propstat":
				addPropstat(resp.props, c)
			}
		}
		out = append(out, resp)
	}
	return out, nil
}

func addPropstat(into map[string]element, ps element) {
	var status string
	var prop element
	for _, c := range ps.Children {
		switch c.Local {
		case "status":
			status = c.Text
		case "prop":
			prop = c
		}
	}
	if !strings.Contains(status, " 200 ") {
		return
	}
	for _, p := range prop.Children {
		into[p.Local] = p
	}
}

func decodeRoot(dec *xml.Decoder) (element, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return element{}, err //nolint:wrapcheck // wrapped by the caller
		}
		if se, ok := tok.(xml.StartElement); ok {
			return parseElement(dec, se, 0)
		}
	}
}

func parseElement(dec *xml.Decoder, start xml.StartElement, depth int) (element, error) {
	if depth > maxElementDepth {
		return element{}, errors.New("document nested too deeply")
	}
	el := element{Space: start.Name.Space, Local: start.Name.Local, Attrs: attrsOf(start)}
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return element{}, err //nolint:wrapcheck // wrapped by the caller
		}
		done, err := el.take(dec, tok, depth, &text)
		if err != nil || done {
			return el, err
		}
	}
}

// take adds one token to the element being read, reporting whether it ended
// the element.
func (e *element) take(dec *xml.Decoder, tok xml.Token, depth int, text *strings.Builder) (bool, error) {
	switch t := tok.(type) {
	case xml.StartElement:
		child, err := parseElement(dec, t, depth+1)
		if err != nil {
			return false, err
		}
		e.Children = append(e.Children, child)
	case xml.CharData:
		_, _ = text.Write(t)
	case xml.EndElement:
		e.Text = strings.TrimSpace(text.String())
		if e.Local == "calendar-data" {
			// Calendar data is line-oriented; keep it as sent.
			e.Text = text.String()
		}
		return true, nil
	}
	return false, nil
}

func attrsOf(start xml.StartElement) map[string]string {
	if len(start.Attr) == 0 {
		return nil
	}
	out := make(map[string]string, len(start.Attr))
	for _, a := range start.Attr {
		out[a.Name.Local] = a.Value
	}
	return out
}
