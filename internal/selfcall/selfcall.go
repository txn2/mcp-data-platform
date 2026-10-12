// Package selfcall carries a request to the platform's own HTTP API that is
// served inside this process on behalf of a caller the platform has already
// authenticated (#2086).
//
// The built-in platform-admin connection acts as its caller: a call that
// carries the caller's token forwards it over the loopback, and the admin and
// portal authenticators check it like any other. A managed script's run has
// no token -- it presents the roles its owner held at the save -- so its call
// is handed to the platform's handler directly, with the identity the
// tool-call middleware authenticated recorded on the request's context. The
// authenticators read it from there. A request that arrives over the network
// cannot carry a context value, so this adds no credential anyone can send.
package selfcall

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

type contextKey struct{}

// With returns ctx marked as a request served inside this process for c.
func With(ctx context.Context, c mcpcontext.Caller) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
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

// Transport sends a request ForCall marked through self, and every other
// request through network.
func Transport(self, network http.RoundTripper) http.RoundTripper {
	return router{self: self, network: network}
}

type router struct {
	self, network http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (r router) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, ok := From(req.Context()); ok {
		return r.self.RoundTrip(req) //nolint:wrapcheck // transport pass-through
	}
	return r.network.RoundTrip(req) //nolint:wrapcheck // transport pass-through
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
