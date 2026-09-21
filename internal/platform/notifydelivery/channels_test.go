package notifydelivery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/internal/notification/notifypost"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/notification/smtp"
)

// stubChannels serves channel records for the test send.
type stubChannels struct {
	channels map[string]notification.Channel
	err      error
}

func (s *stubChannels) List(context.Context) ([]notification.Channel, error) {
	return nil, s.err
}

func (s *stubChannels) Get(_ context.Context, name string) (*notification.Channel, error) {
	if s.err != nil {
		return nil, s.err
	}
	ch, ok := s.channels[name]
	if !ok {
		return nil, notifychannel.ErrChannelNotFound
	}
	return &ch, nil
}

func (*stubChannels) Set(context.Context, notification.Channel) error { return nil }
func (*stubChannels) Delete(context.Context, string) error            { return nil }

// enabledSMTP is a mail server ready to send.
func enabledSMTP() *fakeSettings {
	return &fakeSettings{settings: &smtp.Settings{
		Enabled: true, Host: "smtp.example.com", Port: 587, From: "p@example.com",
	}}
}

func TestChannels_NilSafe(t *testing.T) {
	var h *Handle
	if h.Channels() != nil {
		t.Error("a nil handle returned a channel store")
	}
	if err := h.SendChannelTest(context.Background(), "ops"); err == nil {
		t.Error("a nil handle accepted a test send")
	}
	if err := h.SendDocumentEmail(context.Background(), "a@example.com", notification.Document{Title: "t"}); err == nil {
		t.Error("a nil handle accepted a document email")
	}
}

func TestSendChannelTest_PostsThroughTheKindsTransport(t *testing.T) {
	// The administrator is asking whether this configuration works, so the
	// answer has to be the transport's own -- which means the send bypasses
	// the queue.
	posted := 0
	h := &Handle{
		channels: &stubChannels{channels: map[string]notification.Channel{
			"ops": {
				Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true,
				Mode: notification.ChannelModeImmediate, Connection: "c", Target: "C1",
			},
		}},
		channelSenders: notifypost.NewSenders(func(string) (notifypost.Upstream, error) {
			posted++
			return nil, errors.New("upstream unreachable in this test")
		}),
	}
	err := h.SendChannelTest(context.Background(), "ops")
	if err == nil {
		t.Fatal("the test reported success though the upstream refused")
	}
	if !strings.Contains(err.Error(), "ops") {
		t.Errorf("error %q does not name the channel", err)
	}
	if posted != 1 {
		t.Errorf("the transport was reached %d times, want 1", posted)
	}
}

func TestSendChannelTest_DisabledChannelIsStillTested(t *testing.T) {
	// An administrator is configuring it; refusing to test until it is
	// enabled would mean enabling it untested.
	reached := false
	h := &Handle{
		channels: &stubChannels{channels: map[string]notification.Channel{
			"ops": {
				Name: "ops", Kind: notification.ChannelKindWebhook, Enabled: false,
				Mode: notification.ChannelModeImmediate, Connection: "c",
			},
		}},
		channelSenders: notifypost.NewSenders(func(string) (notifypost.Upstream, error) {
			reached = true
			return nil, errors.New("unreachable")
		}),
	}
	_ = h.SendChannelTest(context.Background(), "ops")
	if !reached {
		t.Error("a disabled channel was not tested")
	}
}

func TestSendChannelTest_AbsentChannel(t *testing.T) {
	h := &Handle{channels: &stubChannels{channels: map[string]notification.Channel{}}}
	err := h.SendChannelTest(context.Background(), "gone")
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("err = %v, want a failure naming the channel", err)
	}
}

func TestSendChannelTest_ChatKindWithoutAGateway(t *testing.T) {
	// A deployment whose api gateway is not wired cannot reach a chat
	// channel's connection, and says so rather than failing obscurely.
	h := &Handle{channels: &stubChannels{channels: map[string]notification.Channel{
		"ops": {Name: "ops", Kind: notification.ChannelKindMattermost, Enabled: true, Connection: "c", Target: "C1"},
	}}}
	err := h.SendChannelTest(context.Background(), "ops")
	if err == nil || !strings.Contains(err.Error(), "api gateway") {
		t.Errorf("err = %v, want a statement that no gateway is wired", err)
	}
}

func TestSendChannelTest_EmailKindSendsToEveryRecipient(t *testing.T) {
	// The list is what is being tested, and one bad address among twenty is
	// exactly the defect a test exists to surface.
	sender := &captureSender{}
	h := &Handle{
		channels: &stubChannels{channels: map[string]notification.Channel{
			"ops-email": {
				Name: "ops-email", Kind: notification.ChannelKindEmail, Enabled: true,
				Mode:       notification.ChannelModeImmediate,
				Recipients: []string{"a@example.com", "b@example.com"},
			},
		}},
		settings: enabledSMTP(),
		renderer: testRenderer(t),
		sender:   sender,
	}
	if err := h.SendChannelTest(context.Background(), "ops-email"); err != nil {
		t.Fatalf("SendChannelTest: %v", err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("sent %d emails, want one per recipient", len(sender.sent))
	}
	if sender.sent[0].To != "a@example.com" || sender.sent[1].To != "b@example.com" {
		t.Errorf("recipients = %q, %q", sender.sent[0].To, sender.sent[1].To)
	}
	if !strings.Contains(sender.sent[0].Subject, "Test message") {
		t.Errorf("subject = %q, want the test document's title", sender.sent[0].Subject)
	}
}

func TestSendChannelTest_EmailKindReportsTheFirstFailure(t *testing.T) {
	h := &Handle{
		channels: &stubChannels{channels: map[string]notification.Channel{
			"ops-email": {
				Name: "ops-email", Kind: notification.ChannelKindEmail, Enabled: true,
				Recipients: []string{"a@example.com"},
			},
		}},
		settings: enabledSMTP(),
		renderer: testRenderer(t),
		sender:   &captureSender{err: errors.New("mailbox full")},
	}
	err := h.SendChannelTest(context.Background(), "ops-email")
	if err == nil || !strings.Contains(err.Error(), "a@example.com") {
		t.Errorf("err = %v, want a failure naming the recipient", err)
	}
}

func TestSendDocumentEmail_RendersTheDocumentAsABrandedEmail(t *testing.T) {
	sender := &captureSender{}
	h := &Handle{settings: enabledSMTP(), renderer: testRenderer(t), sender: sender}
	doc := notification.Document{
		Title: "Weekly report", Body: "Revenue rose 4%.",
		Link: "https://portal.example.com/portal/assets/a1",
	}
	if err := h.SendDocumentEmail(context.Background(), "reader@example.com", doc); err != nil {
		t.Fatalf("SendDocumentEmail: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(sender.sent))
	}
	email := sender.sent[0]
	if email.Subject != "Weekly report" {
		t.Errorf("subject = %q, want the document's own title", email.Subject)
	}
	if !strings.Contains(email.Text, "Revenue rose 4%.") {
		t.Errorf("the body did not reach the message: %q", email.Text)
	}
	if !strings.Contains(email.HTML, doc.Link) {
		t.Error("the link did not reach the message")
	}
}

func TestSendDocumentEmail_ReportsAnUnusableMailServer(t *testing.T) {
	h := &Handle{
		settings: &fakeSettings{err: smtp.ErrNotFound},
		renderer: testRenderer(t), sender: &captureSender{},
	}
	if err := h.SendDocumentEmail(context.Background(), "a@example.com", notification.Document{Title: "t"}); err == nil {
		t.Error("a document was reported sent with no mail server configured")
	}
}

func TestTestDocument_NamesTheChannel(t *testing.T) {
	doc := testDocument("ops")
	if doc.Title == "" {
		t.Error("the test document has no title")
	}
	if !strings.Contains(doc.Body, "ops") {
		t.Errorf("the test document does not name the channel: %q", doc.Body)
	}
}
