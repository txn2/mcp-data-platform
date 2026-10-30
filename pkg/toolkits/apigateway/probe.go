package apigateway

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/txn2/mcp-data-platform/internal/connprobe"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
)

// probePath is the address a probe asks for when the connection's catalog
// declares no read operation it can call: the connection's own base, with no
// operation named. It is a GET, so it is safe by definition.
const probePath = "/"

// ProbeConnection sends the named connection's upstream one GET through the
// same client, authenticator and TLS material a tool call uses, and reports
// the status that came back (#1805).
//
// The GET is a read operation the connection's catalog declares, taking no
// path parameter and no required parameter, when there is one; otherwise the
// base URL (#2368). A route the catalog declares is one the credential is
// meant to reach, so a 401 or 403 there is the upstream rejecting the
// credential. The base is not: a key scoped to a few operations is refused
// at "/" while working everywhere it is meant to, so a 403 there says only
// that the credential cannot reach "/". A 401 is a rejected credential
// wherever it comes from.
//
// A session_login connection signs in before the GET is sent, and a sign-in
// that fails is reported as such, so a GET that was answered at all means the
// sign-in returned a session: the credential works, and the test says so.
//
// Implements connprobe.Prober.
func (t *Toolkit) ProbeConnection(ctx context.Context, name string) connprobe.Result {
	c, ok := t.lookup(name)
	if !ok {
		return connprobe.Failure(fmt.Sprintf("connection %q is not served by this toolkit", name), nil)
	}
	// A connection resolved by an in-process handler has no upstream and a
	// synthetic base URL, so dialing it would report the platform's own
	// built-in connection as unreachable.
	if c.cfg.Handler == HandlerInternal {
		return connprobe.Success(fmt.Sprintf(
			"connection %q is served in this process by the platform itself, so there is no upstream to reach", name))
	}
	path, declared := probeOperation(c.specs)
	req, err := buildUpstreamRequest(ctx, c.cfg, c.auth, catalogView{}, InvokeInput{
		Method: http.MethodGet,
		Path:   path,
	})
	if err != nil {
		// Building the request is also where the credential is obtained, so an
		// OAuth token endpoint that cannot be reached surfaces here rather than
		// on the call below.
		return connprobe.Failure(fmt.Sprintf("connection %q could not build an authorized request", name), err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		err = outbound.ScrubError(req.Context(), err)
		return connprobe.Failure(upstreamauth.UnreachedSubject(name, c.cfg.BaseURL, err), err)
	}
	defer func() { _ = resp.Body.Close() }()
	return connprobe.JudgeGET(connprobe.GET{
		Prefix: upstreamauth.GrantDetail(c.auth), Base: c.cfg.BaseURL, Path: path, Declared: declared,
		SignedIn: c.cfg.AuthMode == upstreamauth.AuthModeSessionLogin, Status: resp.StatusCode,
	})
}

// probeOperation returns the path of the read operation a probe asks for: a
// GET the catalog declares with no path parameter and no required parameter,
// the shortest path first and then in order, so the same one is asked for on
// every test. ok is false when the catalog declares none, and the probe asks
// for the base.
func probeOperation(specs map[string]*specState) (path string, ok bool) {
	paths := make([]string, 0, len(specs))
	for _, st := range specs {
		paths = append(paths, callableReads(st)...)
	}
	if len(paths) == 0 {
		return probePath, false
	}
	slices.SortFunc(paths, func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.Count(a, pathSep), strings.Count(b, pathSep)), cmp.Compare(a, b))
	})
	return paths[0], true
}

// callableReads returns the paths, under the spec's base path, of the GETs a
// spec declares that take no path parameter and no required parameter.
func callableReads(st *specState) []string {
	if st == nil || st.doc == nil || st.doc.Paths == nil {
		return nil
	}
	var paths []string
	for raw, item := range st.doc.Paths.Map() {
		if item == nil || item.Get == nil || strings.Contains(raw, "{") ||
			anyRequired(item.Parameters) || anyRequired(item.Get.Parameters) {
			continue
		}
		paths = append(paths, st.effectiveBasePath+raw)
	}
	return paths
}

// anyRequired reports whether a parameter list names a required parameter.
func anyRequired(params openapi3.Parameters) bool {
	for _, p := range params {
		if p != nil && p.Value != nil && p.Value.Required {
			return true
		}
	}
	return false
}

// Verify interface compliance.
var _ connprobe.Prober = (*Toolkit)(nil)
