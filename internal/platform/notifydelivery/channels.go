package notifydelivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Channels returns the store the operator's channel records live in, or nil
// when the feature is unavailable (no database).
func (h *Handle) Channels() notification.ChannelStore {
	if h == nil {
		return nil
	}
	return h.channels
}

// testDocument is what a test send delivers: enough for the administrator to
// recognize it in the destination and nothing they would have to clean up.
func testDocument(name string) notification.Document {
	return notification.Document{
		Title: "Test message from the data platform",
		Body: fmt.Sprintf("This is a test of the %q notification channel. "+
			"Receiving it confirms the channel's connection, credential and target are correct.", name),
	}
}

// SendChannelTest delivers a test message to one channel immediately and
// reports what its upstream said.
//
// It bypasses the queue deliberately, and unlike every other send in this
// package that is not a shortcut but the point: an administrator pressing
// "send test" is asking whether this configuration works, so the answer has to
// be the transport's own, now, in the response they are waiting on. A queued
// test would report only that a row was written, which is the one thing never
// in doubt.
//
// A disabled channel is still tested: the administrator is configuring it, and
// refusing to test until it is enabled would mean enabling it untested.
func (h *Handle) SendChannelTest(ctx context.Context, name string) error {
	if h == nil {
		return errors.New("notification channels unavailable: no database configured")
	}
	ch, err := h.channels.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("reading channel %q: %w", name, err)
	}
	if ch.Kind == notification.ChannelKindEmail {
		return h.sendChannelTestEmails(ctx, *ch)
	}
	if h.channelSenders == nil {
		return errors.New("channel delivery is unavailable: no api gateway is wired to reach a channel's connection")
	}
	sendCtx, cancel := context.WithTimeout(ctx, channelTestTimeout)
	defer cancel()
	if err := h.channelSenders.Send(sendCtx, *ch, testDocument(name)); err != nil {
		return fmt.Errorf("channel %q: %w", name, err)
	}
	return nil
}

// channelTestTimeout bounds a test send. It is shorter than a connection's own
// call timeout because an administrator is watching the response: an upstream
// that has not answered in this long has answered the question.
const channelTestTimeout = 20 * time.Second

// sendChannelTestEmails delivers the test to each address on an email
// channel's list through the mail server, reporting the first failure.
//
// It sends to every recipient rather than to the first: the list is what is
// being tested, and one bad address among twenty is exactly the defect a test
// exists to surface.
func (h *Handle) sendChannelTestEmails(ctx context.Context, ch notification.Channel) error {
	doc := testDocument(ch.Name)
	for _, addr := range ch.Recipients {
		to := notification.NormalizeAddress(addr)
		if to == "" {
			continue
		}
		if err := h.SendDocumentEmail(ctx, to, doc); err != nil {
			return fmt.Errorf("channel %q recipient %s: %w", ch.Name, to, err)
		}
	}
	return nil
}

// SendDocumentEmail renders one document as a branded email and sends it
// directly, outside the queue. It is the transactional counterpart of a
// queued channel row, used by the admin test send.
func (h *Handle) SendDocumentEmail(ctx context.Context, to string, doc notification.Document) error {
	if h == nil {
		return errors.New("notifications unavailable: no database configured")
	}
	settings, err := h.smtpSettings(ctx)
	if err != nil {
		return err
	}
	email, err := h.renderer.Render([]notification.Notification{{
		Recipient: to,
		Category:  notification.CategoryChannel,
		Payload: notification.Payload{
			Kind:      notification.KindChannel,
			ItemTitle: doc.Title,
			Link:      doc.Link,
			Document:  &doc,
		},
	}})
	if err != nil {
		return fmt.Errorf("rendering channel email: %w", err)
	}
	if err := h.sender.Send(ctx, *settings, *email); err != nil {
		return fmt.Errorf("sending channel email: %w", err)
	}
	return nil
}
