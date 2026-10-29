package connprobe

import (
	"fmt"
	"net/http"
)

// GET is one probing GET an HTTP-based connection kind sent and the status it
// was answered with (#2368).
type GET struct {
	// Prefix opens the detail: what the credential exchange reported, or "".
	Prefix string
	// Base is the connection's base URL and Path what was asked for under it.
	Base, Path string
	// Declared is whether Path is an operation the connection's catalog
	// declares, which makes it a route the credential is meant to reach.
	Declared bool
	// SignedIn is whether a sign-in returned a session before the GET was
	// sent, which proves the credential on its own.
	SignedIn bool
	// Status is the upstream's answer.
	Status int
}

// JudgeGET reads a probing GET's status. A 401, and a 403 on a declared
// operation, is the upstream rejecting the credential. A 403 on a route the
// catalog does not declare says only that the credential cannot reach it: a
// key scoped to a few operations is refused at the base while working where it
// is meant to. Any other answer proves the route and the credential.
func JudgeGET(g GET) Result {
	detail := g.Prefix
	if g.SignedIn {
		detail += "the sign-in returned a session, so the credential works; "
	}
	detail += fmt.Sprintf("%s answered GET %s with HTTP %d", g.Base, g.Path, g.Status)
	switch {
	case g.Status == http.StatusUnauthorized, g.Status == http.StatusForbidden && g.Declared:
		subject, where := "this connection's credential", "rather than the route"
		if g.SignedIn {
			subject = "the session the sign-in returned"
		}
		if g.Declared {
			where = "on an operation its catalog declares"
		}
		return Failure(fmt.Sprintf("%s, which is the upstream rejecting %s %s", detail, subject, where), nil)
	case g.Status == http.StatusForbidden:
		return Success(detail + ": the credential cannot reach " + g.Path +
			", which the connection's catalog does not declare, so the upstream answered and the credential was not refused on a route it is meant to reach")
	case g.Declared:
		return Success(detail + "; the operation is one the connection's catalog declares, so the answer proves the route and the credential")
	}
	return Success(detail + "; the status is the upstream's own answer at its base, and any answer proves the route and the credential")
}
