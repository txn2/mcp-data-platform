package apigateway

import (
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/pathtemplate"
)

// cfgKeyRequiredPathPrefix names the path every raw method+path call on a
// connection must start with (Config.RequiredPathPrefix, #1707).
const cfgKeyRequiredPathPrefix = "required_path_prefix"

// validateRequiredPathPrefix holds the prefix to the shape a path has: it
// starts with "/" and carries no query, fragment, or segment validatePath would
// refuse. A trailing slash was trimmed at parse, so "/" is no prefix at all.
func (c Config) validateRequiredPathPrefix() error {
	if strings.Contains(c.RequiredPathPrefix, "?") {
		return fmt.Errorf("apigateway: %s %q must be a path such as \"/api/v1\"", cfgKeyRequiredPathPrefix, c.RequiredPathPrefix)
	}
	return validatePathKey(cfgKeyRequiredPathPrefix, c.RequiredPathPrefix, "/api/v1")
}

// validatePathKey holds a configuration key naming a path to the shape a
// caller's path has (pathtemplate.CheckConfigPath). Empty is unset.
func validatePathKey(key, p, example string) error {
	err := pathtemplate.CheckConfigPath(p)
	if p == "" || err == nil {
		return nil
	}
	if errors.Is(err, pathtemplate.ErrNotAPath) {
		return fmt.Errorf("apigateway: %s %q must be a path such as %q", key, p, example)
	}
	return fmt.Errorf("apigateway: %s %q %w", key, p, err)
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

// baseURLCarriesPrefix and withinPathPrefix are the prefix rules
// pathtemplate holds.
var (
	baseURLCarriesPrefix = pathtemplate.BaseCarriesPrefix
	withinPathPrefix     = pathtemplate.WithinPrefix
)
