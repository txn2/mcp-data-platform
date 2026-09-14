package notifyrender

import (
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// connAuthLinkText labels the revocation alert's button. The generic label
// would quote the connection name, which reads as a place rather than as the
// action the recipient came to perform.
const connAuthLinkText = "Reauthorize the connection"

// connAssertionLinkText labels the button on a refused-assertion alert. There
// is nothing on the connection page to reauthorize: the reader goes there to
// read which key, subject and token endpoint the connection uses.
const connAssertionLinkText = "Open the connection"

// connAuthLinkTextFor is the button label for an alert.
func connAuthLinkTextFor(c *notification.ConnectionAuth) string {
	if c != nil && c.SignedAssertion {
		return connAssertionLinkText
	}
	return connAuthLinkText
}

// Reasons the platform reached the verdict without the upstream answering. The
// email says so rather than reporting a rejection nobody made: "your
// authorization was rejected" and "your authorization ran out" ask the reader
// for the same click but describe different events, and only one of them is a
// reason to go and look at the upstream.
const (
	reasonNoRefreshToken = "no_refresh_token"
	reasonRefreshExpired = "refresh_expired"
)

// connAuthSubject is the subject and heading of a revoked-credential alert
// (#1694). It names the connection, because that is what the recipient
// authorized and what they will go and reconnect.
//
// A payload with no revocation still renders a meaningful line: a queue row
// outlives the revocation that wrote it, so a row enqueued by one build may be
// delivered by another.
func connAuthSubject(c *notification.ConnectionAuth) string {
	if c == nil || c.Name == "" {
		return "A connection needs to be reauthorized"
	}
	if c.SignedAssertion {
		return fmt.Sprintf("The connection %q cannot get an access token", c.Name)
	}
	if c.Escalated {
		return fmt.Sprintf("The connection %q is still unauthorized", c.Name)
	}
	return fmt.Sprintf("The connection %q needs to be reauthorized", c.Name)
}

// connAuthBody is the alert's prose body: what happened, what the upstream
// said, and what is true of the connection until somebody reconnects it. It
// renders unquoted (emailItem.Body) because the platform is speaking here, not
// a colleague.
func connAuthBody(c *notification.ConnectionAuth) string {
	if c == nil {
		return ""
	}
	if c.SignedAssertion {
		return connAssertionBody(c)
	}
	sentences := []string{connAuthOpening(c)}
	if !c.RevokedAt.IsZero() {
		sentences = append(sentences,
			fmt.Sprintf("That was at %s.", c.RevokedAt.UTC().Format("2006-01-02 15:04 UTC")))
	}
	sentences = append(sentences, connAuthConsequence(c))
	return strings.Join(sentences, " ")
}

// connAuthOpening is the first sentence: what the platform did and what the
// upstream said about it, or that the upstream was never called.
//
// The subject is the ATTEMPT rather than the platform, because every outcome
// clause is predicated on the attempt ("was rejected by", "could not be made").
// The escalation names the operator in front of it, since its reader is
// somebody else and the first thing they need is whose authorization lapsed.
func connAuthOpening(c *notification.ConnectionAuth) string {
	attempt := "The platform's attempt to renew this connection's access"
	if c.Escalated {
		attempt = fmt.Sprintf("%s authorized this connection, and the platform's attempt to renew its access",
			connAuthOperator(c))
	}
	return attempt + " " + connAuthOutcome(c) + "."
}

// connAuthOutcome is the clause describing how the renewal ended.
func connAuthOutcome(c *notification.ConnectionAuth) string {
	switch c.Reason {
	case reasonNoRefreshToken:
		return "could not be made: the connection holds nothing to renew with"
	case reasonRefreshExpired:
		return "could not be made: the window the upstream gave for renewing had already closed"
	}
	at := "the upstream"
	if c.IDPHost != "" {
		at = c.IDPHost
	}
	if c.Reason == "" {
		return "was rejected by " + at
	}
	return fmt.Sprintf("was rejected by %s, which answered %s", at, c.Reason)
}

// connAuthOperator names the person whose authorization lapsed, for the
// escalation, where the recipient is somebody else.
func connAuthOperator(c *notification.ConnectionAuth) string {
	if c.AuthorizedBy == "" {
		return "Somebody"
	}
	return c.AuthorizedBy
}

// connAuthConsequence is the closing sentence: what the connection does now,
// and — on the escalation — why this arrived instead of being handled.
func connAuthConsequence(c *notification.ConnectionAuth) string {
	const stopped = "The platform discarded the credential, so every call through this connection now " +
		"fails needing reauthorization, including the scheduled ones nobody is watching."
	if !c.Escalated {
		return stopped + " Reconnecting it restores access; nothing else has to be changed."
	}
	waited := "the escalation window"
	if c.EscalatedAfterHours > 0 {
		waited = countOf(c.EscalatedAfterHours, "hour")
	}
	return fmt.Sprintf("%s It has been %s and the connection has not been reauthorized, "+
		"which is why this reached you.", stopped, waited)
}

// connAssertionBody is the body of a refused jwt_bearer assertion alert
// (#1734). It differs from the revocation's in what it asks for: nothing was
// discarded and nothing can be reconnected, so it names what the upstream said
// and the three causes an operator fixes at the upstream, and says the alert
// clears itself.
func connAssertionBody(c *notification.ConnectionAuth) string {
	at := "The upstream token endpoint"
	if c.IDPHost != "" {
		at = "The token endpoint at " + c.IDPHost
	}
	opening := at + " refused the signed assertion this connection exchanges for its access token"
	switch {
	case c.Reason != "" && c.Description != "":
		opening += fmt.Sprintf(", answering %s: %q.", c.Reason, c.Description)
	case c.Reason != "":
		opening += ", answering " + c.Reason + "."
	default:
		opening += "."
	}
	sentences := []string{opening}
	if !c.RevokedAt.IsZero() {
		sentences = append(sentences,
			fmt.Sprintf("That was at %s.", c.RevokedAt.UTC().Format("2006-01-02 15:04 UTC")))
	}
	sentences = append(sentences,
		"Every call through this connection fails until the upstream accepts the assertion again, "+
			"including the scheduled ones nobody is watching.",
		"There is nothing to reconnect: the usual causes are a signing key or an integration user the upstream "+
			"has not approved, or a clock that disagrees with the upstream's.",
		"The platform signs a new assertion on the next call, and this alert clears the first time the upstream accepts one.")
	return strings.Join(sentences, " ")
}
