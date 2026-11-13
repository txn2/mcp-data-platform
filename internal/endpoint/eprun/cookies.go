package eprun

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// Application cookies (#2555): a handler on an application's own address
// reads req["cookies"] and sets cookies by returning "cookies". The platform
// writes each under a fixed prefix and strips it on the way in, so a handler
// only ever sees and sets cookies with that prefix: the platform's own cookies
// (page_viewer, page_state, a portal session) are out of its reach under any
// casing, since a name is matched exactly.
const (
	// CookiePrefixSecure is the prefix over https: a __Host- cookie has no
	// Domain, Path=/ and Secure, so no sibling origin can set it.
	CookiePrefixSecure = "__Host-app-"
	// CookiePrefixPlain is the prefix over http, where __Host- is refused.
	CookiePrefixPlain = "app-"

	// MaxCookieNameLen, MaxCookieValueBytes, MaxCookiesSet and MaxCookiesRead
	// bound what a handler sets and reads; MaxCookieAge is the browsers' own
	// cap, 400 days.
	MaxCookieNameLen    = 32
	MaxCookieValueBytes = 1024
	MaxCookiesSet       = 10
	MaxCookiesRead      = 20
	MaxCookieAge        = 400 * 24 * 60 * 60

	// SameSiteLax is a cookie's default; SameSiteStrict the other it may be.
	// None is refused: an application cookie is never sent cross-site.
	SameSiteLax    = "Lax"
	SameSiteStrict = "Strict"

	// cookieRedaction names what replaces a cookie value in an audit row.
	cookieRedaction = "cookie"
)

var cookieName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Cookie is one application cookie a handler sets.
type Cookie struct {
	Name  string
	Value string
	// MaxAge is nil for a session cookie, 0 to delete the cookie, and
	// otherwise its lifetime in seconds.
	MaxAge   *int
	SameSite string
}

// ReadCookies is the application cookies on a request, the prefix taken off,
// at most MaxCookiesRead of them, the first in name order. Only a name that
// starts with prefix exactly is the application's.
func ReadCookies(prefix string, names, values []string) map[string]string {
	all := map[string]string{}
	for i, name := range names {
		if short, ok := strings.CutPrefix(name, prefix); ok && cookieName.MatchString(short) {
			if _, seen := all[short]; !seen {
				all[short] = values[i]
			}
		}
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := map[string]string{}
	for _, k := range keys[:min(len(keys), MaxCookiesRead)] {
		out[k] = all[k]
	}
	return out
}

// cookieRedactor holds the request's cookie values, which no audit row and no
// kept failure may hold (#2555). A value shorter than a stored secret's
// minimum is left alone: replacing it would rewrite ordinary text.
func cookieRedactor(cookies map[string]string) *secretref.Redactor {
	red := &secretref.Redactor{}
	for _, v := range cookies {
		if len(v) >= secretref.MinValueLength {
			red.Add(cookieRedaction, v)
		}
	}
	return red
}

// cookiesValue is req["cookies"]: the application cookies by name, empty
// where the request carries none.
func cookiesValue(cookies map[string]string) *starlark.Dict {
	d := starlark.NewDict(len(cookies))
	for _, k := range sortedKeys(cookies) {
		_ = d.SetKey(starlark.String(k), starlark.String(cookies[k]))
	}
	return d
}

// cookiesFrom reads the "cookies" a handler returned: a list of dicts of
// name, value, max_age and same_site.
func cookiesFrom(v starlark.Value) ([]Cookie, error) {
	list, ok := v.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("cookies is %s; it is a list of dicts of name, value, max_age and same_site", typeName(v))
	}
	if list.Len() > MaxCookiesSet {
		return nil, fmt.Errorf("cookies sets %d cookies; a response sets at most %d", list.Len(), MaxCookiesSet)
	}
	out := make([]Cookie, 0, list.Len())
	for i := range list.Len() {
		c, err := cookieFrom(list.Index(i))
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(out, func(o Cookie) bool { return o.Name == c.Name }) {
			return nil, fmt.Errorf("cookie %q is set twice in one response", c.Name)
		}
		out = append(out, c)
	}
	return out, nil
}

// cookieFrom reads one cookie a handler returned.
func cookieFrom(v starlark.Value) (Cookie, error) {
	d, ok := v.(*starlark.Dict)
	if !ok {
		return Cookie{}, fmt.Errorf("a cookie is %s; it is a dict of name, value, max_age and same_site", typeName(v))
	}
	c := Cookie{SameSite: SameSiteLax}
	for _, item := range d.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return Cookie{}, errors.New("a cookie's keys are name, value, max_age and same_site")
		}
		if err := c.read(string(key), item[1]); err != nil {
			return Cookie{}, err
		}
	}
	if !cookieName.MatchString(c.Name) {
		return Cookie{}, fmt.Errorf("cookie name %q: 1 to %d letters, digits, _ and -", c.Name, MaxCookieNameLen)
	}
	return c, nil
}

// read takes one key of a cookie dict onto c.
func (c *Cookie) read(key string, v starlark.Value) error {
	switch key {
	case "name":
		s, ok := v.(starlark.String)
		if !ok {
			return fmt.Errorf("cookie name is %s; it is a string", typeName(v))
		}
		c.Name = string(s)
	case "value":
		return c.readValue(v)
	case "max_age":
		return c.readMaxAge(v)
	case "same_site":
		return c.readSameSite(v)
	default:
		return fmt.Errorf("a cookie has the key %q; its keys are name, value, max_age and same_site", key)
	}
	return nil
}

func (c *Cookie) readSameSite(v starlark.Value) error {
	s, ok := v.(starlark.String)
	switch {
	case !ok:
		return fmt.Errorf("cookie same_site is %s; it is \"Lax\" or \"Strict\"", typeName(v))
	case strings.EqualFold(string(s), SameSiteLax):
		c.SameSite = SameSiteLax
	case strings.EqualFold(string(s), SameSiteStrict):
		c.SameSite = SameSiteStrict
	default:
		return fmt.Errorf("cookie same_site %q: \"Lax\" or \"Strict\"; an application cookie is never sent with a cross-site request", string(s))
	}
	return nil
}

func (c *Cookie) readValue(v starlark.Value) error {
	s, ok := v.(starlark.String)
	if !ok {
		return fmt.Errorf("cookie value is %s; it is a string", typeName(v))
	}
	if len(s) > MaxCookieValueBytes {
		return fmt.Errorf("cookie value is %d bytes; a value is at most %d", len(s), MaxCookieValueBytes)
	}
	if i := strings.IndexFunc(string(s), func(r rune) bool { return !cookieOctet(r) }); i >= 0 {
		return errors.New("cookie value holds a character a cookie cannot carry (a space, a quote, a comma, a semicolon, " +
			"a backslash or a control character); encode it first, as base64 or hex")
	}
	c.Value = string(s)
	return nil
}

func (c *Cookie) readMaxAge(v starlark.Value) error {
	n, ok := v.(starlark.Int)
	if !ok {
		return fmt.Errorf("cookie max_age is %s; it is seconds, 0 to delete the cookie", typeName(v))
	}
	age, exact := n.Int64()
	if !exact || age < 0 || age > MaxCookieAge {
		return fmt.Errorf("cookie max_age %s: from 0 (delete the cookie) to %d seconds, 400 days", n, MaxCookieAge)
	}
	a := int(age)
	c.MaxAge = &a
	return nil
}

// cookieOctet reports whether r is an RFC 6265 cookie-octet: printable ASCII
// but space, double quote, comma, semicolon and backslash.
func cookieOctet(r rune) bool {
	return r > asciiSpace && r < asciiDelete && r != '"' && r != ',' && r != ';' && r != '\\'
}

// asciiSpace and asciiDelete bound printable ASCII, both excluded.
const (
	asciiSpace  = 0x20
	asciiDelete = 0x7f
)
