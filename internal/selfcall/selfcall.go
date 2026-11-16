// Package selfcall carries a request to the platform's own HTTP API that is
// served inside this process on behalf of a caller the platform has already
// authenticated (#2086).
//
// Every call on the built-in platform-admin connection is served by the
// platform's own handler in this process, and its request is marked as made by
// a tool call (ByTool), whether or not it carries the caller's token (#2656).
// That marker says who is asking -- an agent, not the person at a browser --
// and grants nothing: the routes that make an agent's move wait on a person
// read it, and nothing authenticates by it. A call that carries the caller's
// token keeps it, and the admin and portal authenticators check it like any
// other. A managed script's run has no token -- it presents the roles its
// owner held at the save -- so the identity the tool-call middleware
// authenticated is recorded on the request's context too (With), and the
// authenticators read it from there. A request that arrives over the network
// cannot carry a context value, so neither adds a credential anyone can send.
package selfcall

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

type (
	contextKey struct{}
	toolKey    struct{}
)

// With returns ctx marked as a request served inside this process for c, and
// as made by a tool call.
func With(ctx context.Context, c mcpcontext.Caller) context.Context {
	return context.WithValue(MarkTool(ctx), contextKey{}, c)
}

// MarkTool returns ctx marked as a request a tool call made to the platform's
// own API. It names no one and authenticates nothing.
func MarkTool(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolKey{}, true)
}

// ByTool reports whether a request was made by a tool call (an agent's call on
// the platform-admin connection, a managed script's run, a tool that serves
// itself through the API) rather than by a person's own client. It is the one
// answer every "is this an agent" check reads (#2656).
func ByTool(ctx context.Context) bool {
	marked, _ := ctx.Value(toolKey{}).(bool)
	return marked
}

// From returns the caller a request served inside this process was made for,
// and false for every other request.
func From(ctx context.Context) (mcpcontext.Caller, bool) {
	c, ok := ctx.Value(contextKey{}).(mcpcontext.Caller)
	return c, ok && c.UserID != ""
}

// Handler is the platform's own HTTP handler, set once the server has
// assembled it. The connection that dispatches to it is registered before the
// server exists, so it holds this and the server fills it in.
type Handler struct {
	h atomic.Pointer[http.Handler]
}

// Set installs the handler requests are served by.
func (s *Handler) Set(h http.Handler) {
	s.h.Store(&h)
}

// ServeHTTP serves r with the installed handler, or answers 503 when the
// server has not installed one: a process serving no HTTP API.
func (s *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := s.h.Load()
	if h == nil {
		http.Error(w, "the platform's HTTP API is not served by this process", http.StatusServiceUnavailable)
		return
	}
	(*h).ServeHTTP(w, r)
}

// ForCall marks req to be served inside this process when the tool call it
// belongs to was authenticated as someone and carries no token to forward: a
// managed script's run. It reports false, leaving req alone, otherwise.
func ForCall(ctx context.Context, req *http.Request) (*http.Request, bool) {
	caller, ok := mcpcontext.GetCaller(ctx)
	if !ok || mcpcontext.GetAuthToken(ctx) != "" {
		return req, false
	}
	return req.WithContext(With(req.Context(), caller)), true
}

// Transport serves every request through self, the platform's own handler,
// marked as made by a tool call: the transport of the platform-admin
// connection, every request of which is a tool call's (#2656).
func Transport(self http.RoundTripper) http.RoundTripper {
	return marker{self: self}
}

type marker struct {
	self http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (m marker) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.self.RoundTrip(req.WithContext(MarkTool(req.Context()))) //nolint:wrapcheck // transport pass-through
}

// PersonaError refuses a caller whose roles do not resolve to the persona a
// surface requires. Its text names the persona, the roles that reach it, and
// the roles the caller presents.
type PersonaError struct {
	Surface string
	Persona string
	Need    []string
	Caller  string
	Has     []string
}

func (e *PersonaError) Error() string {
	return fmt.Sprintf("the %s requires the persona %q, which the roles %v reach; %s presents %v",
		e.Surface, e.Persona, e.Need, e.Caller, e.Has)
}

// Install hands h to every toolkit among kits that serves a connection with
// the platform's own handler (the api kind's platform-admin connection).
func Install[T any](kits []T, h http.Handler) {
	for _, k := range kits {
		if s, ok := any(k).(interface{ SetSelfHandler(h http.Handler) }); ok {
			s.SetSelfHandler(h)
		}
	}
}
