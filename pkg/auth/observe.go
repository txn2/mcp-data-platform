package auth

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// UserObserver is notified of each successful authentication so the platform
// can record the person in the known-users directory (#614). Implementations
// MUST be non-blocking and best-effort: the observer runs on the authentication
// hot path and must never block or fail it.
type UserObserver func(*middleware.UserInfo)

// ObservingAuthenticator wraps an Authenticator and invokes an observer on each
// successful authentication. Authentication behavior is otherwise unchanged —
// the observer never affects the returned result or error.
type ObservingAuthenticator struct {
	inner   middleware.Authenticator
	observe UserObserver
}

// NewObservingAuthenticator wraps inner so that observe is called with the
// resolved user on every successful Authenticate.
func NewObservingAuthenticator(inner middleware.Authenticator, observe UserObserver) *ObservingAuthenticator {
	return &ObservingAuthenticator{inner: inner, observe: observe}
}

// Authenticate delegates to the wrapped authenticator and, on success, notifies
// the observer.
func (a *ObservingAuthenticator) Authenticate(ctx context.Context) (*middleware.UserInfo, error) {
	info, err := a.inner.Authenticate(ctx)
	if err == nil && info != nil && a.observe != nil {
		a.observe(info)
	}
	return info, err //nolint:wrapcheck // transparent decorator: preserve the inner error verbatim
}

// Verify interface compliance.
var _ middleware.Authenticator = (*ObservingAuthenticator)(nil)
