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
	// At is when the credential was discarded.
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

// announceRevoked tells the wired sink, if any. Nil-safe on both the Writer and
// the sink so the caller never branches.
//
// The sink is read under the lock and called outside it: an implementation
// bounds its own work, but it does real work, and holding the Writer's lock
// across it would serialize every concurrent revocation behind the first.
func (w *Writer) announceRevoked(ctx context.Context, rev Revocation) {
	if w == nil {
		return
	}
	w.mu.RLock()
	sink := w.revocations
	w.mu.RUnlock()
	if sink == nil {
		return
	}
	sink.Revoked(ctx, rev)
}
