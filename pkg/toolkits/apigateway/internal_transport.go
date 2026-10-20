package apigateway

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/handlertransport"
	"github.com/txn2/mcp-data-platform/internal/outbound"
)

// This file implements the handler=internal dispatch (issue #1005):
// a connection whose http.Client round-trips into an in-process
// http.Handler instead of dialing an upstream. The seam sits at the
// transport so everything downstream of client.Do — the buffered
// invoke read, the export stream-to-S3 path, size and timeout caps,
// audit, metrics instrumentation — treats an internal connection
// exactly like a network one.

// SetInternalHandler wires the in-process handler that
// handler=internal connections dispatch to. Must be called before
// such a connection is added; AddConnection fails otherwise, because
// a connection that can never serve a request should not register.
func (t *Toolkit) SetInternalHandler(h http.Handler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.internalHandler = h
}

// newInternalHTTPClient builds the dispatch client for a
// handler=internal connection. Unlike newHTTPClient it sets NO
// client-level Timeout: the per-call context already bounds every
// path (api_invoke_endpoint via resolveTimeout, api_export via
// resolveExportTimeout), and http.Client.Timeout also bounds body
// READING — so a client timeout of cfg.CallTimeout (60s default)
// would cap a large fetch_url export at the call timeout and defeat
// api_export's own multi-minute timeout. Bounding through the context
// lets an export run for its configured export timeout while an inline
// invoke stays bounded by the shorter invoke timeout. Redirects are
// not followed here; the internal handler owns its own redirect policy
// (fetch_url's follow_redirects) and returns a final response.
func newInternalHTTPClient(name string, h http.Handler) *http.Client {
	return outbound.NewClient(outbound.Options{Kind: outbound.KindAPI, Connection: name, Base: handlertransport.New(h)})
}

// SetSelfHandler installs the platform's own HTTP handler, which a
// self_dispatch connection serves a token-less call with (#2086).
func (t *Toolkit) SetSelfHandler(h http.Handler) { t.selfHandler.Set(h) }

// ScriptRunCannotServe reports whether connection acts as its caller toward
// an upstream other than this platform, which a saved script's run, having no
// caller token, cannot call (#2086). False for an unknown connection.
func (t *Toolkit) ScriptRunCannotServe(connection string) bool {
	c, ok := t.lookup(connection)
	return ok && c.cfg.IdentityPassthrough && !c.cfg.SelfDispatch
}
