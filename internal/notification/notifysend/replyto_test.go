package notifysend

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/notification/notifyrender"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// TestBuildMessage_ReplyTo proves the rendered Reply-To reaches the wire and
// that an unset one leaves the header off entirely (#1023).
func TestBuildMessage_ReplyTo(t *testing.T) {
	email := notifyrender.Email{To: "a@b.io", Subject: "Hello", Text: "plain", HTML: "<p>h</p>", ReplyTo: "support@example.com"}

	msg, err := buildMessage(smtp.Settings{From: "p@example.com"}, email)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	var out strings.Builder
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if !strings.Contains(out.String(), "Reply-To: <support@example.com>") {
		t.Errorf("message missing Reply-To header:\n%s", out.String())
	}

	email.ReplyTo = ""
	msg, err = buildMessage(smtp.Settings{From: "p@example.com"}, email)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	out.Reset()
	if _, err := msg.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if strings.Contains(out.String(), "Reply-To") {
		t.Error("unset reply_to must emit no Reply-To header")
	}
}

func TestBuildMessage_InvalidReplyTo(t *testing.T) {
	_, err := buildMessage(
		smtp.Settings{From: "p@example.com"},
		notifyrender.Email{To: "a@b.io", Subject: "s", Text: "t", HTML: "<p>h</p>", ReplyTo: "not an address"})
	if err == nil {
		t.Fatal("expected error for invalid reply-to")
	}
}
