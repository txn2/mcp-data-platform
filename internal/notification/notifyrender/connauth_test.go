package notifyrender

import (
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// revokedNotification is the alert an upstream's rejection raises.
func revokedNotification(mutate func(*notification.ConnectionAuth)) notification.Notification {
	conn := &notification.ConnectionAuth{
		Kind: "api", Name: "billing", IDPHost: "idp.example.com",
		Reason: "invalid_grant", AuthorizedBy: "ops@example.com",
		RevokedAt: time.Date(2026, 9, 12, 8, 30, 0, 0, time.UTC),
	}
	if mutate != nil {
		mutate(conn)
	}
	return notification.Notification{
		Recipient: "ops@example.com",
		Payload: notification.Payload{
			Kind: notification.KindConnectionAuth, ItemID: "api/billing",
			ItemTitle: "billing", Connection: conn,
			Link: "https://platform.example.com/portal/admin/connections",
		},
	}
}

// TestConnAuthSubject pins the line an inbox shows: the connection, named,
// because that is what the recipient authorized and will go and reconnect.
func TestConnAuthSubject(t *testing.T) {
	got := Subject(revokedNotification(nil))
	if !strings.Contains(got, "billing") || !strings.Contains(got, "reauthorized") {
		t.Errorf("subject must name the connection and what it needs, got %q", got)
	}

	esc := Subject(revokedNotification(func(c *notification.ConnectionAuth) { c.Escalated = true }))
	if !strings.Contains(esc, "still") {
		t.Errorf("the escalation must read as a connection nobody came back to, got %q", esc)
	}
	if esc == got {
		t.Error("the escalation and the first alert must not claim the same thing")
	}

	bare := connAuthSubject(nil)
	if bare == "" {
		t.Error("a payload from an older build must still render a meaningful line")
	}
}

// TestConnAuthBody pins what the mail actually tells the reader: which upstream
// rejected it, what it said, when, and what the connection does now.
func TestConnAuthBody(t *testing.T) {
	item := buildItem(revokedNotification(nil))
	if item.Message != "" {
		t.Error("the revocation detail must not render as a quotation")
	}
	for _, want := range []string{"idp.example.com", "invalid_grant", "2026-09-12 08:30 UTC", "reauthorization"} {
		if !strings.Contains(item.Body, want) {
			t.Errorf("body must carry %q, got %q", want, item.Body)
		}
	}
	// Every outcome clause is predicated on the ATTEMPT ("was rejected by",
	// "could not be made"), so the sentence has to open with the attempt. The
	// first draft opened with "The platform tried to renew this connection's
	// access", which reads "...access was rejected by idp.example.com".
	if !strings.HasPrefix(item.Body, "The platform's attempt to renew this connection's access was rejected by") {
		t.Errorf("the opening sentence does not parse: %q", item.Body)
	}
	if item.LinkText != connAuthLinkText {
		t.Errorf("the button must name the action, got %q", item.LinkText)
	}
	if connAuthBody(nil) != "" {
		t.Error("a payload with no revocation renders no body")
	}
}

// TestConnAuthBody_LocalVerdicts is the honesty criterion: the platform must not
// report a rejection nobody made. Two of the three causes never reach the
// upstream, and the mail says so.
func TestConnAuthBody_LocalVerdicts(t *testing.T) {
	tests := []struct {
		reason   string
		want     string
		unwanted string
	}{
		{reasonNoRefreshToken, "nothing to renew with", "rejected"},
		{reasonRefreshExpired, "had already closed", "rejected"},
	}
	for _, tt := range tests {
		body := connAuthBody(revokedNotification(func(c *notification.ConnectionAuth) {
			c.Reason = tt.reason
		}).Payload.Connection)
		if !strings.Contains(body, tt.want) {
			t.Errorf("%s: body must say %q, got %q", tt.reason, tt.want, body)
		}
		if strings.Contains(body, tt.unwanted) {
			t.Errorf("%s: body claims a rejection that never happened: %q", tt.reason, body)
		}
	}
}

// TestConnAuthBody_Escalation pins what the second alert adds: whose
// authorization lapsed, and why this reached somebody else.
func TestConnAuthBody_Escalation(t *testing.T) {
	body := connAuthBody(revokedNotification(func(c *notification.ConnectionAuth) {
		c.Escalated = true
		c.EscalatedAfterHours = 24
	}).Payload.Connection)
	for _, want := range []string{"ops@example.com", "24 hours", "reached you"} {
		if !strings.Contains(body, want) {
			t.Errorf("escalation body must carry %q, got %q", want, body)
		}
	}

	if !strings.HasPrefix(body, "ops@example.com authorized this connection, and the platform's attempt to renew its access was rejected by") {
		t.Errorf("the escalation's opening sentence does not parse: %q", body)
	}

	anon := connAuthBody(revokedNotification(func(c *notification.ConnectionAuth) {
		c.Escalated = true
		c.AuthorizedBy = ""
	}).Payload.Connection)
	if strings.Contains(anon, "  ") || !strings.Contains(anon, "Somebody") {
		t.Errorf("an unattributed connection must still read as a sentence, got %q", anon)
	}
}

// TestConnAuthOutcome_UnknownUpstream covers the two degraded shapes a queued
// row can reach the renderer in.
func TestConnAuthOutcome_UnknownUpstream(t *testing.T) {
	noHost := connAuthOutcome(&notification.ConnectionAuth{Reason: "invalid_client"})
	if !strings.Contains(noHost, "the upstream") {
		t.Errorf("a revocation with no host must still name who rejected it, got %q", noHost)
	}
	noReason := connAuthOutcome(&notification.ConnectionAuth{IDPHost: "idp.example.com"})
	if !strings.Contains(noReason, "idp.example.com") || strings.Contains(noReason, "answered") {
		t.Errorf("a revocation with no reason must not invent one, got %q", noReason)
	}
}
