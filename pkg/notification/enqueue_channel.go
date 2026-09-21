package notification

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// NotifyChannel queues doc for delivery to ch and reports how many rows were
// written.
//
// The three HTTP kinds write one row addressed to the channel. The email kind
// writes one row per recipient instead of one row for the list, so a person on
// an operator's list keeps everything a person has: their own ModeOff, their
// own digest window, and the unsubscribe link in the footer. That is the same
// choice the review-queue alert makes for its operator-named recipients, and
// for the same reason -- a distribution list is not a mailbox, and treating it
// as one would make one person's opt-out silence everyone.
//
// The actor's rate limit is charged once for the whole send, not once per
// recipient: the size of an email channel's list is the operator's choice, not
// the sender's, so a script posting to a twenty-address channel must not
// exhaust a budget that exists to bound the addresses a sender picks.
//
// A zero return with a nil error means the send was accepted and nothing was
// queued -- every recipient on an email channel had opted out, or the actor
// was over their rate limit. A caller reporting to a person should say so
// rather than reporting a send.
func (e *Enqueuer) NotifyChannel(ctx context.Context, ch Channel, actor string, doc Document) (int, error) {
	if e == nil {
		return 0, nil
	}
	if !ch.Enabled {
		return 0, fmt.Errorf("channel %q is disabled", ch.Name)
	}
	if err := ValidateChannel(ch); err != nil {
		return 0, fmt.Errorf("channel %q is misconfigured: %w", ch.Name, err)
	}
	if err := ValidateDocument(doc); err != nil {
		return 0, err
	}
	if !e.allowActor(actor, ch.Name) {
		return 0, nil
	}
	if ch.Kind == ChannelKindEmail {
		return e.enqueueChannelEmails(ctx, ch, actor, doc)
	}
	return e.enqueueChannelRow(ctx, ch, actor, doc)
}

// enqueueChannelRow writes the single row an HTTP-kind channel delivers
// through. It consults no preference store: the recipient is a destination
// rather than a person, so there is nobody whose preferences could apply.
func (e *Enqueuer) enqueueChannelRow(ctx context.Context, ch Channel, actor string, doc Document) (int, error) {
	n := Notification{
		Recipient: ch.Recipient(),
		Category:  CategoryChannel,
		Channel:   ch.Name,
		Payload:   channelPayload(actor, doc),
	}
	e.applyChannelSchedule(&n, ch, false)
	if err := e.queue.Enqueue(ctx, n); err != nil {
		return 0, fmt.Errorf("enqueueing document for channel %s: %w", logsan.SanitizeForLog(ch.Name), err)
	}
	return 1, nil
}

// enqueueChannelEmails fans an email channel out to one row per recipient,
// applying each person's own preferences and dropping those who opted out.
func (e *Enqueuer) enqueueChannelEmails(ctx context.Context, ch Channel, actor string, doc Document) (int, error) {
	payload := channelPayload(actor, doc)
	queued := 0
	for _, addr := range ch.Recipients {
		recipient := NormalizeAddress(addr)
		if recipient == "" || !Deliverable(recipient) {
			continue
		}
		prefs, err := e.prefs.Get(ctx, recipient)
		if err != nil {
			return queued, fmt.Errorf("reading notification prefs for %s: %w", logsan.SanitizeForLog(recipient), err)
		}
		if prefs.Mode == ModeOff {
			continue
		}
		n := Notification{
			Recipient: recipient,
			Category:  CategoryChannel,
			Channel:   ch.Name,
			Payload:   payload,
		}
		e.applyChannelSchedule(&n, ch, prefs.Mode == ModeDaily)
		if err := e.queue.Enqueue(ctx, n); err != nil {
			return queued, fmt.Errorf("enqueueing document for channel %s: %w", logsan.SanitizeForLog(ch.Name), err)
		}
		queued++
	}
	return queued, nil
}

// applyChannelSchedule puts the row in the right delivery window. The
// channel's daily mode batches every recipient; a recipient's own daily mode
// batches that recipient on an immediate channel. Either alone is enough,
// because each is somebody's stated choice to receive one message a day.
func (e *Enqueuer) applyChannelSchedule(n *Notification, ch Channel, recipientWantsDigest bool) {
	if ch.Mode != ChannelModeDaily && !recipientWantsDigest {
		return
	}
	n.Digest = true
	n.ScheduledFor = NextDigestTime(e.now().UTC(), e.digestHourUTC)
}

// channelPayload wraps a document as the payload a KindChannel row carries.
func channelPayload(actor string, doc Document) Payload {
	return Payload{
		Kind:      KindChannel,
		ItemTitle: doc.Title,
		Actor:     NormalizeAddress(actor),
		Link:      doc.Link,
		Document:  &doc,
	}
}
