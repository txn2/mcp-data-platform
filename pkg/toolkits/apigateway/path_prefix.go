package apigateway

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// cfgKeyRequiredPathPrefix names the path every raw method+path call on a
// connection must start with (Config.RequiredPathPrefix, #1707).
const cfgKeyRequiredPathPrefix = "required_path_prefix"

// validateRequiredPathPrefix holds the prefix to the shape a path has: it
// starts with "/" and carries no query, fragment, or segment validatePath would
// refuse. A trailing slash was trimmed at parse, so "/" is no prefix at all.
func (c Config) validateRequiredPathPrefix() error {
	p := c.RequiredPathPrefix
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, pathSep) || strings.ContainsAny(p, "?#") {
		return fmt.Errorf("apigateway: %s %q must be a path such as \"/api/v1\"", cfgKeyRequiredPathPrefix, p)
	}
	if err := validatePath(p); err != nil {
		return fmt.Errorf("apigateway: %s %q is not a valid path: %s", cfgKeyRequiredPathPrefix, p, strings.TrimPrefix(err.Error(), "apigateway: "))
	}
	return nil
}

// checkRequiredPathPrefix refuses a raw path outside the connection's required
// prefix, before the route policy or the upstream sees it. The refusal names
// the path with the prefix added and, when the catalog declares an operation
// there, its operation_id: the id an agent reads out of api_discover or search
// is the spec-relative form ("GET /admin/tools"), so the raw path that looks
// like it is exactly the one that is missing the prefix.
//
// A path validatePath refuses is left to it: prefixing "admin/tools" or
// "//host" would suggest a path that is wrong in a second way, and the
// request pipeline refuses the original with the reason that applies. A
// base_url whose own path already ends with the prefix supplies it, so raw
// paths on that connection are relative to it and none is refused.
func (c *conn) checkRequiredPathPrefix(method, path string) error {
	prefix := c.cfg.RequiredPathPrefix
	if prefix == "" || baseURLCarriesPrefix(c.cfg.BaseURL, prefix) || validatePath(path) != nil ||
		withinPathPrefix(stripQueryAndFragment(path), prefix) {
		return nil
	}
	suggested := prefix + path
	msg := fmt.Sprintf("apigateway: connection %q serves its routes under %q, and path %q is outside it, so the request was not sent. Send path %q",
		c.cfg.ConnectionName, prefix, path, suggested)
	upper := strings.ToUpper(strings.TrimSpace(method))
	if id := c.resolveViaRouter(upper, stripQueryAndFragment(suggested)); id != "" {
		msg += fmt.Sprintf(", or operation_id %q as api_discover lists it", id)
	}
	return errors.New(msg + ".")
}

// baseURLCarriesPrefix reports whether the base URL's path ends with prefix,
// the same test computeEffectiveBasePath applies before dropping a spec's
// server path. The prefix starts with "/", so the match is on a segment
// boundary: "/xapi/v1" does not end with "/api/v1".
func baseURLCarriesPrefix(baseURL, prefix string) bool {
	u, err := url.Parse(baseURL)
	return err == nil && strings.HasSuffix(strings.TrimSuffix(u.Path, pathSep), prefix)
}

// withinPathPrefix reports whether path is prefix itself or lies beneath it on
// a segment boundary, so "/api/v10" is not inside "/api/v1".
func withinPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+pathSep)
}
