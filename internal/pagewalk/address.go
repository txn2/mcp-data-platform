package pagewalk

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"
)

// pathSep is the URL path separator.
const pathSep = "/"

// Target is where one page is requested from: the path and query joined
// to the connection's base URL, and the request body. An address writes
// all three on every page, holding the ones it does not move, so the
// gateway applies a Target without knowing which kind produced it.
type Target struct {
	Path  string
	Query map[string]any
	Body  any
}

// Address is where a walk requests its next page from. A proxied
// connection's address is the path and query joined to base_url; the
// built-in util connection's fetch_url address is the url inside the
// request body, because that is the document being paged. The walk is
// one implementation over both; only the place the next page is written
// to differs.
type Address interface {
	// FollowURL moves the address to a next link. It refuses a link whose
	// scheme or host differs from the one the walk is pinned to.
	FollowURL(next string) error
	// Param reads a query parameter of the address ("" when unset).
	Param(name string) string
	// SetParam sets a query parameter of the address.
	SetParam(name, value string)
	// Target is the request the address currently points at.
	Target() Target
}

// AddressSpec is what NewAddress builds an Address from: the first page's
// request, the connection's base URL, whether the url in the body is the
// document walked (the util connection's fetch_url), and the path rule a
// followed link must satisfy (the gateway's validatePath).
type AddressSpec struct {
	BaseURL      string
	Path         string
	Query        map[string]any
	Body         any
	WalkBodyURL  bool
	ValidatePath func(string) error
}

// NewAddress picks the address for a walk. With WalkBodyURL set and a
// body carrying a url string, the walk moves that url; everything else
// is addressed by path and query.
func NewAddress(spec AddressSpec) (Address, error) {
	if spec.WalkBodyURL {
		if body, ok := bodyObject(spec.Body); ok {
			if raw, ok := body["url"].(string); ok {
				return newFetchAddress(spec, body, raw)
			}
		}
	}
	return newPathAddress(spec)
}

// bodyObject returns the request body as an object: one sent as a map,
// or one sent as a string of JSON that decodes to a map. The gateway
// accepts a body in either form (a string that parses as JSON passes
// through as JSON), so a fetch_url body is walked in either.
func bodyObject(body any) (map[string]any, bool) {
	switch b := body.(type) {
	case map[string]any:
		return b, true
	case string:
		var m map[string]any
		if err := json.Unmarshal([]byte(b), &m); err == nil && m != nil {
			return m, true
		}
	}
	return nil, false
}

// pathAddress addresses a page by the path and query joined to the
// connection's base URL, the same join the gateway's buildURL performs.
type pathAddress struct {
	base         *url.URL
	path         string
	query        map[string]any
	body         any
	validatePath func(string) error
}

func newPathAddress(spec AddressSpec) (*pathAddress, error) {
	base, err := url.Parse(spec.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("apigateway: base_url %q must include scheme and host", spec.BaseURL)
	}
	validate := spec.ValidatePath
	if validate == nil {
		validate = func(string) error { return nil }
	}
	return &pathAddress{base: base, path: spec.Path, query: cloneQuery(spec.Query), body: spec.Body, validatePath: validate}, nil
}

// FollowURL pins the next link to the connection's host and re-derives
// the path relative to base_url, so the request the walk builds goes
// through the same buildURL join and validatePath checks as the first
// page, and the route policy sees the path it would see on a direct
// call. The link's scheme is not compared: the page is requested through
// the connection's base_url whatever the link says, and an API behind a
// TLS-terminating proxy writes its links with the scheme it sees inside
// (#1543).
func (a *pathAddress) FollowURL(next string) error {
	u, err := pinnedNextURL(a.base, next, false)
	if err != nil {
		return err
	}
	rel, err := pathUnderBase(a.base.Path, u.Path)
	if err != nil {
		return err
	}
	if err := a.validatePath(rel); err != nil {
		return err
	}
	a.path = rel
	a.query = queryToMap(u.Query())
	return nil
}

// Param implements Address.
func (a *pathAddress) Param(name string) string {
	v, ok := a.query[name]
	if !ok {
		return ""
	}
	return ScalarString(firstScalar(v))
}

// SetParam implements Address.
func (a *pathAddress) SetParam(name, value string) {
	if a.query == nil {
		a.query = map[string]any{}
	}
	a.query[name] = value
}

// Target implements Address; the body is the first page's, unchanged.
func (a *pathAddress) Target() Target {
	return Target{Path: a.path, Query: a.query, Body: a.body}
}

// fetchAddress addresses a page by the url in a fetch_url body. The walk
// is pinned to the host of the url it started on; the util handler's
// own destination guard still runs on every page.
type fetchAddress struct {
	u     *url.URL
	body  map[string]any
	path  string
	query map[string]any
}

func newFetchAddress(spec AddressSpec, body map[string]any, raw string) (*fetchAddress, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("apigateway: body.url must be an absolute URL to walk")
	}
	return &fetchAddress{u: u, body: maps.Clone(body), path: spec.Path, query: spec.Query}, nil
}

// FollowURL implements Address, pinned to the scheme and host of the
// first url: a fetched page is requested at the link itself, so a link
// that changes the scheme would change the wire.
func (a *fetchAddress) FollowURL(next string) error {
	u, err := pinnedNextURL(a.u, next, true)
	if err != nil {
		return err
	}
	a.u = u
	return nil
}

// Param implements Address over the url's query.
func (a *fetchAddress) Param(name string) string {
	return a.u.Query().Get(name)
}

// SetParam implements Address over the url's query.
func (a *fetchAddress) SetParam(name, value string) {
	q := a.u.Query()
	q.Set(name, value)
	a.u.RawQuery = q.Encode()
}

// Target implements Address; the path and query are the first page's,
// unchanged, and the body carries the moved url.
func (a *fetchAddress) Target() Target {
	a.body["url"] = a.u.String()
	return Target{Path: a.path, Query: a.query, Body: a.body}
}

// pinnedNextURL parses a next link and refuses one that would move the
// walk to another host, or, with matchScheme, to another scheme. The
// refusal names where the link pointed so the caller can see it without
// the walk having sent it anything. Userinfo is refused outright: a
// credential in a link the upstream chose is not one this connection
// holds.
func pinnedNextURL(pin *url.URL, next string, matchScheme bool) (*url.URL, error) {
	u, err := url.Parse(next)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("apigateway: next link is not an absolute URL: %q", next)
	}
	if u.User != nil {
		return nil, errors.New("apigateway: next link carries userinfo; refusing")
	}
	if !strings.EqualFold(u.Host, pin.Host) {
		return nil, fmt.Errorf("apigateway: next link points at %s://%s; the walk is pinned to %s", u.Scheme, u.Host, pin.Host)
	}
	if matchScheme && !strings.EqualFold(u.Scheme, pin.Scheme) {
		return nil, fmt.Errorf("apigateway: next link points at %s://%s; the walk is pinned to %s://%s", u.Scheme, u.Host, pin.Scheme, pin.Host)
	}
	return u, nil
}

// pathUnderBase returns the part of a next link's path below the
// connection's base path, so joining it back onto base_url reproduces
// the link. A link outside the base path is refused: the connection was
// registered for what lives under its base_url, and a route policy
// written against those paths must see them as written.
func pathUnderBase(basePath, linkPath string) (string, error) {
	base := strings.TrimSuffix(basePath, pathSep)
	if base == "" {
		return linkPath, nil
	}
	if linkPath == base {
		return pathSep, nil
	}
	if !strings.HasPrefix(linkPath, base+pathSep) {
		return "", fmt.Errorf("apigateway: next link path %q is outside the connection's base path %q", linkPath, basePath)
	}
	return strings.TrimPrefix(linkPath, base), nil
}

// queryToMap converts a parsed query into the map the gateway's buildURL
// takes, keeping a repeated parameter as one entry per value.
func queryToMap(values url.Values) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for k, vs := range values {
		if len(vs) == 1 {
			out[k] = vs[0]
			continue
		}
		list := make([]any, len(vs))
		for i, v := range vs {
			list[i] = v
		}
		out[k] = list
	}
	return out
}

func cloneQuery(q map[string]any) map[string]any {
	if q == nil {
		return nil
	}
	return maps.Clone(q)
}

// nextPage decides from a page's signal how the walk reaches the page
// after it, moves the address there, and returns the signal it acted on
// (nil when the walk is at its end). A next URL wins over a cursor, a
// cursor over page numbering, because each is more specific than the
// one below it about where the next page is. The one refusal is a cursor
// the walk has no way to send back: the caller named neither a
// cursor_param to carry it nor a page_param to number past it.
func nextPage(addr Address, p PaginateInput, sig *PaginationInfo, pageNo int) (*PaginationInfo, error) {
	switch {
	case sig != nil && sig.NextURL != "":
		return sig, addr.FollowURL(sig.NextURL) //nolint:wrapcheck // the address names the refusal
	case sig != nil && sig.NextCursor != "" && p.CursorParam != "":
		addr.SetParam(p.CursorParam, sig.NextCursor)
		return sig, nil
	case p.PageParam != "":
		return advancePageParam(addr, p)
	case sig != nil && sig.NextCursor != "":
		return nil, fmt.Errorf("apigateway: page %d carries a cursor (%s) but paginate names no cursor_param to send it as", pageNo, sig.Source)
	}
	return nil, nil //nolint:nilnil // no signal and no page parameter: the walk is at its end
}

// advancePageParam steps the page parameter and reports the step as a
// signal, so a walk stopped at max_pages hands back the page number it
// would have requested next.
func advancePageParam(addr Address, p PaginateInput) (*PaginationInfo, error) {
	cur, err := strconv.Atoi(addr.Param(p.PageParam))
	if err != nil {
		return nil, fmt.Errorf("apigateway: paginate.page_param %q is not an integer on the request: %w", p.PageParam, err)
	}
	next := strconv.Itoa(cur + p.PageStep)
	addr.SetParam(p.PageParam, next)
	return &PaginationInfo{HasMore: true, NextCursor: next, Source: sourcePageParam}, nil
}

// FinalCursor is the cursor or link that addressed the last page of a
// walk, for provenance. Empty on a one-page walk.
func FinalCursor(lead *PaginationInfo) string {
	if lead == nil {
		return ""
	}
	if lead.NextURL != "" {
		return lead.NextURL
	}
	return lead.NextCursor
}
