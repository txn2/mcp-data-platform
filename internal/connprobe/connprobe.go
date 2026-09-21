// Package connprobe is the contract for opening one of a toolkit's connections
// on request and reporting what its upstream said.
//
// It is its own package rather than a corner of pkg/toolkit because it shares
// nothing with the connection types there: those describe what a toolkit
// reports about a connection without being asked, and this is a request that
// reaches an upstream. The cohesion gate reads that as two packages, and it is
// right — a caller that wants to enumerate connections does not want this, and
// the one that wants this does not need a listing.
package connprobe

import (
	"context"
	"regexp"
)

// Result is what one active connection check found.
//
// It is deliberately three fields and not a status enum: an operator asking
// "does this connection work" is asking one yes/no question, and everything
// else they need is prose about what answered. Detail describes a success
// specifically enough to be worth reading (which catalog answered, how many
// buckets are visible, what status the upstream returned), because a bare
// "ok: true" against the wrong credential looks identical to the right one.
type Result struct {
	// OK is whether the connection answered.
	OK bool `json:"ok"`
	// Detail says what answered, on success and on failure alike.
	Detail string `json:"detail,omitempty"`
	// Error is the failure as the upstream or the client reported it, empty
	// when OK.
	Error string `json:"error,omitempty"`
}

// Prober is an optional interface for toolkits that can actively
// verify one of their connections: open it, ask it something harmless, and
// report what came back.
//
// It exists because a connection could be created, stored, listed and read
// back without anything ever having opened it, so an unusable connection was
// indistinguishable from a working one until a real call failed — which for a
// scheduled script is the middle of a pipeline (#1805). The health that
// list_connections reports is not this: that is observed passively from traffic
// the platform happened to forward, so an idle connection has no health to
// report and a never-called one never had any.
//
// A probe must be READ-ONLY and cheap. It runs on an operator's request against
// production credentials, so it asks the upstream the smallest question that
// proves the credential and the route: a trivial query, a listing, an
// introspection, a tools/list.
type Prober interface {
	// ProbeConnection checks one connection by its bound name. An unknown
	// name is a failed result naming it, not an error: the caller is asking
	// about a connection, and "this toolkit does not serve it" is an answer.
	ProbeConnection(ctx context.Context, name string) Result
}

// urlCredentials matches the userinfo of a URL — the "user:secret@" a DSN
// carries — in free error text.
var urlCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@"']*:[^\s/@"']*@`)

// Failure builds a failed result from an error, so every kind's failure
// reads the same way.
//
// The error text is scrubbed of URL credentials before it leaves. An upstream's
// own words are the point of reporting a failure, and some of those words are a
// DSN: a Trino connection whose URL will not parse answers with the URL it
// tried, userinfo included. That would put a password in a response body whose
// sibling GET redacts the same value, so the one place every kind's failure is
// built is where it is taken out.
func Failure(detail string, err error) Result {
	r := Result{Detail: detail}
	if err != nil {
		r.Error = urlCredentials.ReplaceAllString(err.Error(), "${1}[REDACTED]@")
	}
	return r
}

// Success builds a successful result.
func Success(detail string) Result {
	return Result{OK: true, Detail: detail}
}
