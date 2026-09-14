package authevents

import (
	"context"
	"time"
)

// Revocation is one connection losing its stored credential: which connection,
// whose authorization it was, which upstream rejected it, what the upstream
// said, and when.
//
// It is the same incident the TypeTokenDeletedRevoked row records, told to
// somebody rather than filed. The history row answers an operator who already
// knows to ask; this is for the person who does not (#1694).
type Revocation struct {
	// Kind is the connection kind (mcp, api, graphql).
	Kind string
	// Name is the connection name within the kind.
	Name string
	// AuthorizedBy is the identity that authorized the connection — the person
	// whose one-time browser sign-in is what has now lapsed. It is the whole
	// reason a sink is told rather than left to read the event row: by the
	// time anything could query for it, the token row carrying it is deleted.
	AuthorizedBy string
	// IDPHost is the host of the upstream token endpoint. Empty when the
	// platform reached the verdict without calling it.
	IDPHost string
	// Reason is the stable short form recorded in the event detail: an RFC 6749
	// error code when the upstream answered, and no_refresh_token or
	// refresh_expired when it was never called.
	Reason string
	// Description is the upstream's error_description, when it sent one. The
	// refresh path leaves it empty; the jwt_bearer exchange carries it, because
	// the operator fixes that refusal at the upstream and the description is
	// usually the only statement of which approval is missing.
	Description string
	// SignedAssertion marks a refusal of an assertion the platform signs with a
	// key the operator registered (oauth_grant jwt_bearer), rather than of a
	// stored authorization somebody granted in a browser. Nobody authorized it,
	// so AuthorizedBy is empty and there is nothing to reconnect: the fix is at
	// the upstream, and the platform retries on the next call.
	SignedAssertion bool
	// At is when the credential was discarded, or the assertion refused.
	At time.Time
}

// RevocationSink is told about a revocation as it is recorded. The platform
// wires one that alerts the person who authorized the connection; a deployment
// with no notification substrate wires none.
//
// A sink must not fail the revocation it is told about: it returns nothing, and
// an implementation that cannot do its work logs and returns.
type RevocationSink interface {
	// Revoked announces one revocation. It is called synchronously on the
	// refresh path, so an implementation that does real work bounds it.
	Revoked(ctx context.Context, rev Revocation)
	// Restored announces that a connection holds a working credential again
	// without anybody reconnecting it: a jwt_bearer exchange the upstream
	// accepted. A connection reconnected through the OAuth callback is cleared
	// there instead. Called synchronously on the exchange path, so an
	// implementation bounds it, and on every accepted exchange, so an
	// implementation with nothing open does nothing expensive.
	Restored(ctx context.Context, kind, name string)
}

// WithRevocations wires the sink told about each revocation this Writer
// records, and returns w so construction chains.
//
// It hangs off the Writer because the Writer is already the one dependency
// every connoauth.Source carries — threaded through the gateway, api and
// graphql toolkits, the shared upstream authenticator, the admin OAuth routes
// and the background refresher. A second dependency announcing the same event
// would be a parallel copy of that whole ladder, and the two could then be
// wired to disagree about which revocations exist. Recording the event and
// announcing it are one fan-out with two consumers.
//
// The sink is wired late — the notification substrate it needs is assembled by
// the HTTP composition root, after the token store and the background refresher
// this Writer already serves — so the field is guarded: the refresher is reading
// it from its own goroutine by the time the root sets it.
//
// nil is accepted and announces nothing.
func (w *Writer) WithRevocations(sink RevocationSink) *Writer {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.revocations = sink
	return w
}

// AssertionRefusal is one refusal of a jwt_bearer connection's signed
// assertion by the upstream token endpoint.
type AssertionRefusal struct {
	// Kind and Name identify the connection.
	Kind string
	Name string
	// TokenURL is the endpoint that refused; only its host is announced.
	TokenURL string
	// Code is the RFC 6749 error code.
	Code string
	// Description is the upstream's error_description, which the caller has
	// already bounded and scrubbed.
	Description string
}

// AssertionRejected announces that the upstream token endpoint refused the
// signed assertion a jwt_bearer connection exchanges for its access token. It
// writes no event row: there is no stored credential whose history it belongs
// to, and the sink's open alert is the record an operator is sent to.
func (w *Writer) AssertionRejected(ctx context.Context, refusal AssertionRefusal) {
	w.announceRevoked(ctx, Revocation{
		Kind:            refusal.Kind,
		Name:            refusal.Name,
		IDPHost:         idpHostOf(refusal.TokenURL),
		Reason:          refusal.Code,
		Description:     refusal.Description,
		SignedAssertion: true,
		At:              time.Now().UTC(),
	})
}

// AssertionAccepted announces that a jwt_bearer exchange succeeded, so an
// alert opened by an earlier refusal is closed by the call that proves the fix
// rather than by a reconnect that does not exist for this grant.
func (w *Writer) AssertionAccepted(ctx context.Context, kind, name string) {
	if sink := w.sink(); sink != nil {
		sink.Restored(ctx, kind, name)
	}
}

// sink reads the wired RevocationSink under the lock. Nil-safe on the Writer.
func (w *Writer) sink() RevocationSink {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.revocations
}

// announceRevoked tells the wired sink, if any. Nil-safe on both the Writer and
// the sink so the caller never branches.
//
// The sink is read under the lock and called outside it: an implementation
// bounds its own work, but it does real work, and holding the Writer's lock
// across it would serialize every concurrent revocation behind the first.
func (w *Writer) announceRevoked(ctx context.Context, rev Revocation) {
	if sink := w.sink(); sink != nil {
		sink.Revoked(ctx, rev)
	}
}
