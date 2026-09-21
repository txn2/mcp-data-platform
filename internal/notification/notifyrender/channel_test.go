package notifyrender

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// channelNotification is one queued channel document addressed to a person on
// an email channel's list.
func channelNotification(title, body, link string) notification.Notification {
	doc := notification.Document{Title: title, Body: body, Link: link}
	return notification.Notification{
		Recipient: "reader@example.com",
		Category:  notification.CategoryChannel,
		Payload: notification.Payload{
			Kind: notification.KindChannel, ItemTitle: title, Link: link,
			Actor: "sender@example.com", Document: &doc,
		},
	}
}

func TestChannelEmail_SubjectIsTheDocumentsOwnTitle(t *testing.T) {
	// Not "so-and-so sent you ...": a channel document is written to be read
	// on its own, and its title is the sentence composed for this line.
	r, err := NewRenderer(Branding{Name: "ACME Data"})
	if err != nil {
		t.Fatal(err)
	}
	email, err := r.Render([]notification.Notification{
		channelNotification("Weekly revenue report", "Revenue rose 4%.", "https://portal.example.com/a/1"),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if email.Subject != "Weekly revenue report" {
		t.Errorf("Subject = %q, want the document's title unchanged", email.Subject)
	}
	if !strings.Contains(email.Text, "Revenue rose 4%.") {
		t.Errorf("the body did not reach the text part: %q", email.Text)
	}
	if !strings.Contains(email.HTML, "https://portal.example.com/a/1") {
		t.Error("the link did not reach the HTML part")
	}
}

func TestChannelEmail_TheBodyIsProseNotAQuotation(t *testing.T) {
	// Message renders as a quotation, and a report is not something a
	// colleague said.
	n := channelNotification("Alert", "queue depth 812", "")
	n.Payload.Message = "should not be quoted"
	item := buildItem(n)
	if item.Message != "" {
		t.Errorf("Message = %q; a channel document renders as prose", item.Message)
	}
	if item.Body != "queue depth 812" {
		t.Errorf("Body = %q, want the document's body", item.Body)
	}
}

func TestChannelEmail_FallsBackToTheItemTitle(t *testing.T) {
	// A row whose payload lost its document still has a subject rather than
	// an empty line.
	n := channelNotification("Fallback title", "", "")
	n.Payload.Document = nil
	if got := Subject(n); got != "Fallback title" {
		t.Errorf("Subject = %q, want the payload's item title", got)
	}
	if got := channelBody(n.Payload); got != "" {
		t.Errorf("channelBody = %q, want empty for a row with no document", got)
	}
}

func TestChannelEmail_LinkTextIsOmittedWithoutALink(t *testing.T) {
	item := buildItem(channelNotification("No link", "body", ""))
	if item.LinkText != "" {
		t.Errorf("LinkText = %q for a document with no link", item.LinkText)
	}
	withLink := buildItem(channelNotification("Link", "body", "https://example.com"))
	if withLink.LinkText != channelLinkText {
		t.Errorf("LinkText = %q, want %q", withLink.LinkText, channelLinkText)
	}
}
