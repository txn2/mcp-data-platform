package notifydelivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestSendGuestLink(t *testing.T) {
	sender := &captureSender{}
	h := &Handle{
		settings: &fakeSettings{settings: &notification.SMTPSettings{
			Enabled: true, Host: "smtp.example.com", Port: 587, From: "p@example.com",
		}},
		renderer: testRenderer(t),
		sender:   sender,
	}

	link := "https://platform.example.com/portal/view/tok1/guest?otk=abc"
	if err := h.SendGuestLink(context.Background(), "bob@example.com", link); err != nil {
		t.Fatalf("SendGuestLink: %v", err)
	}
	if len(sender.sent) != 1 || sender.sent[0].To != "bob@example.com" {
		t.Fatalf("unexpected sends: %+v", sender.sent)
	}
	if !strings.Contains(sender.sent[0].HTML, link) {
		t.Error("email must carry the one-time link")
	}
}

func TestSendGuestLink_NilHandle(t *testing.T) {
	var h *Handle
	if err := h.SendGuestLink(context.Background(), "a@b.io", "https://x"); err == nil {
		t.Error("nil handle SendGuestLink must error")
	}
}

func TestSendGuestLink_Refusals(t *testing.T) {
	tests := []struct {
		name     string
		settings *fakeSettings
		sender   *captureSender
	}{
		{"settings error", &fakeSettings{err: errors.New("no row")}, &captureSender{}},
		{"disabled", &fakeSettings{settings: &notification.SMTPSettings{Enabled: false, Host: "smtp.example.com"}}, &captureSender{}},
		{"no host", &fakeSettings{settings: &notification.SMTPSettings{Enabled: true}}, &captureSender{}},
		{"send fails", &fakeSettings{settings: &notification.SMTPSettings{Enabled: true, Host: "smtp.example.com"}}, &captureSender{err: errors.New("smtp down")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handle{settings: tt.settings, renderer: testRenderer(t), sender: tt.sender}
			if err := h.SendGuestLink(context.Background(), "a@b.io", "https://x"); err == nil {
				t.Error("expected error")
			}
			if len(tt.sender.sent) != 0 {
				t.Errorf("refusal must not send: %+v", tt.sender.sent)
			}
		})
	}
}

func TestNew_UnsubscribeURLReachesRenderer(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	h, err := New(Config{
		DB: db, Encryptor: passthroughEncryptor{},
		Branding:       notification.Branding{Name: "Test"},
		UnsubscribeURL: func(email string) string { return "https://x/unsub?tok=" + email },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	email, err := h.renderer.Render([]notification.Notification{{
		Recipient: "bob@example.com",
		Category:  notification.CategoryShare,
		Payload:   notification.Payload{Kind: notification.KindAsset, ItemTitle: "R", Actor: "a@b.io"},
	}}, notification.Footer{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(email.Text, "https://x/unsub?tok=bob@example.com") {
		t.Error("configured UnsubscribeURL must reach rendered emails")
	}
}
