package apigateway

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// operationAddressing carries the two mutually-exclusive ways a caller
// can name the operation an invoke/export call targets:
//
//   - operation_id (+ optional spec, + optional path_params): the stable
//     identifier api_get_endpoint_schema and api_list_endpoints already
//     speak. path_params are substituted into the operation's path
//     template so the caller never hand-substitutes /users/{id}.
//   - method + path: the raw addressing for uncataloged calls, where
//     the caller has already substituted any path parameters.
//
// resolve collapses either form to the concrete (method, path) the rest
// of the request pipeline (route policy, validatePath, buildURL)
// consumes, so exactly one place understands the operation_id shortcut
// and the buffered invoke, raw passthrough, and api_export paths cannot
// drift on it (issue #1046).
type operationAddressing struct {
	Method      string
	Path        string
	OperationID string
	Spec        string
	PathParams  map[string]string
}

// resolve returns the concrete (method, path) for the call. In the plain
// method+path mode it returns them unchanged. In the operation_id mode it
// resolves the id against the connection's catalog and substitutes
// path_params into the resolved path template. It is an error to supply
// both addressing modes at once, or to supply path_params without an
// operation_id (there is no template to substitute into).
func (a operationAddressing) resolve(c *conn) (method, path string, err error) {
	if a.OperationID == "" {
		if len(a.PathParams) > 0 {
			return "", "", errors.New("apigateway: path_params requires operation_id; without operation_id, substitute values into path directly")
		}
		if a.Method == "" && a.Path == "" {
			return "", "", errors.New("apigateway: provide operation_id, or method and path")
		}
		return a.Method, a.Path, nil
	}
	if a.Method != "" || a.Path != "" {
		return "", "", errors.New("apigateway: provide either operation_id or method+path, not both")
	}
	return resolveOperationTarget(c, a)
}

// resolveOperationTarget maps an operation_id (with optional spec filter
// and path_params) to the concrete (method, path) a call needs. It reuses
// the same resolveOperation walk api_get_endpoint_schema uses so the id
// that reads a schema is exactly the id that invokes it. Errors are
// caller-actionable: no catalog, id-not-found, ambiguity across specs, or
// a path-parameter mismatch.
func resolveOperationTarget(c *conn, a operationAddressing) (method, path string, err error) {
	if len(c.specs) == 0 {
		return "", "", errors.New("apigateway: connection has no catalog specs; call with method+path instead of operation_id")
	}
	match, candidates := resolveOperation(c, a.OperationID, a.Spec)
	if match == nil {
		if len(candidates) > 1 {
			return "", "", ambiguousOperationError(a.OperationID, candidates)
		}
		return "", "", fmt.Errorf("apigateway: operation_id %q not found in connection catalog", a.OperationID)
	}
	concrete, subErr := substitutePathParams(match.path, a.PathParams)
	if subErr != nil {
		return "", "", subErr
	}
	return match.method, concrete, nil
}

// substitutePathParams replaces each {name} placeholder segment in an
// OpenAPI path template with the matching value from params,
// URL-path-escaping each value so a substituted id cannot smuggle path
// structure into the request. Placeholder matching is whole-segment,
// mirroring the toolkit's other template matchers (isPlaceholderSegment,
// segmentMatches) so the substitution and the resolver agree on what a
// placeholder is. A template with no placeholders returns unchanged.
//
// Errors are returned for a missing required parameter, an empty value
// (which would collapse to an empty path segment), or a stray parameter
// that matches no placeholder (a typo guard — the caller almost always
// meant a different name or a query_params entry).
func substitutePathParams(template string, params map[string]string) (string, error) {
	segs := strings.Split(template, pathSep)
	used := make(map[string]bool, len(params))
	var missing []string
	for i, seg := range segs {
		if !isPlaceholderSegment(seg) {
			continue
		}
		name := seg[1 : len(seg)-1]
		val, ok := params[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if val == "" {
			return "", fmt.Errorf("apigateway: path parameter %q is empty; provide a non-empty value", name)
		}
		segs[i] = url.PathEscape(val)
		used[name] = true
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("apigateway: missing required path parameter(s): %s", strings.Join(missing, ", "))
	}
	if unknown := unusedParams(params, used); len(unknown) > 0 {
		return "", fmt.Errorf("apigateway: path parameter(s) not present in the operation path template: %s", strings.Join(unknown, ", "))
	}
	return strings.Join(segs, pathSep), nil
}

// unusedParams returns the sorted names in params that were not consumed
// by a placeholder. Empty when every supplied parameter matched.
func unusedParams(params map[string]string, used map[string]bool) []string {
	var unknown []string
	for name := range params {
		if !used[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// ambiguousOperationError formats the "operation_id defined in more than
// one spec" case into a caller-actionable error naming the candidate
// specs, so the model can retry with spec=<name>. Mirrors the
// disambiguation contract api_get_endpoint_schema exposes.
func ambiguousOperationError(operationID string, candidates []schemaCandidate) error {
	specs := make([]string, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if seen[c.Spec] {
			continue
		}
		seen[c.Spec] = true
		specs = append(specs, c.Spec)
	}
	sort.Strings(specs)
	return fmt.Errorf("apigateway: operation_id %q is defined in multiple specs (%s); pass spec to disambiguate",
		operationID, strings.Join(specs, ", "))
}
