package notification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// ErrChannelOverCap is returned when a channel has already been sent its
// hourly cap. Nothing is queued and the history records nothing; the sender
// is told, in the error, which cap it met.
var ErrChannelOverCap = errors.New("notification: channel is over its hourly cap")

// ChannelSend is one document sent to one channel.
type ChannelSend struct {
	Channel  Channel
	Document Document
	// Actor is who the send is charged to: the person or script sending.
	// Empty for an alert the platform raised.
	Actor string
	// RepeatAfter lengthens the channel's repeat window for the document's
	// key. A value shorter than the channel's own window is ignored: a
	// sender may make an alert quieter, never louder.
	RepeatAfter time.Duration
}

// ChannelResult is what a send became.
type ChannelResult struct {
	// Queued is how many queue rows were written: one for a chat or webhook
	// channel, one per deliverable recipient for an email channel.
	Queued int
	// Suppressed reports a send absorbed by its key's repeat window: counted
	// on the key, and stated by the next send that goes through.
	Suppressed bool
}

// ChannelGate is what a channel send is checked against before its rows are
// written, in the same transaction that writes them.
type ChannelGate struct {
	// Channel is the channel's name.
	Channel string
	// MaxPerHour is the channel's hourly cap, counted in sends.
	MaxPerHour int
	// Key is the document's suppression key. Empty skips suppression.
	Key string
	// Window is the repeat window the key is measured against.
	Window time.Duration
}

// ChannelOutcome is what the gate decided.
type ChannelOutcome struct {
	// Suppressed reports a send the key absorbed: no row was written.
	Suppressed bool
}

// NotifyChannel queues a document for delivery to a channel.
//
// The three HTTP kinds write one row addressed to the channel. The email kind
// writes one row per recipient instead of one row for the list, so a person on
// an operator's list keeps everything a person has: their own ModeOff, their
// own digest window, and the unsubscribe link in the footer. That is the same
// choice the review-queue alert makes for its operator-named recipients, and
// for the same reason -- a distribution list is not a mailbox, and treating it
// as one would make one person's opt-out silence everyone.
//
// The rows of one send are written in one transaction behind the channel's
// gate: the hourly cap, then the key's repeat window. A send over the cap is
// refused with ErrChannelOverCap; a send inside its key's window writes
// nothing and reports Suppressed. Both are decided by the database, so they
// hold across replicas.
//
// The actor's rate limit is charged once for the whole send, not once per
// recipient: the size of an email channel's list is the operator's choice, not
// the sender's, so a script posting to a twenty-address channel must not
// exhaust a budget that exists to bound the addresses a sender picks.
//
// A zero Queued with a nil error and Suppressed false means the send was
// accepted and nothing was queued -- every recipient on an email channel had
// opted out, or the actor was over their rate limit. A caller reporting to a
// person should say so rather than reporting a send.
func (e *Enqueuer) NotifyChannel(ctx context.Context, s ChannelSend) (ChannelResult, error) {
	if e == nil {
		return ChannelResult{}, nil
	}
	ch := s.Channel
	if err := checkSend(s); err != nil {
		return ChannelResult{}, err
	}
	if !e.allowActor(s.Actor, ch.Name) {
		return ChannelResult{}, nil
	}
	rows, err := e.channelRows(ctx, s)
	if err != nil || len(rows) == 0 {
		return ChannelResult{}, err
	}
	out, err := e.queue.EnqueueChannel(ctx, rows, gateFor(s))
	if errors.Is(err, ErrChannelOverCap) {
		return ChannelResult{}, fmt.Errorf("channel %q has been sent its cap of %d messages in the last hour; "+
			"nothing was queued: %w", ch.Name, ch.HourlyCap(), err)
	}
	if err != nil {
		return ChannelResult{}, fmt.Errorf("enqueueing document for channel %s: %w", logsan.SanitizeForLog(ch.Name), err)
	}
	if out.Suppressed {
		return ChannelResult{Suppressed: true}, nil
	}
	return ChannelResult{Queued: len(rows)}, nil
}

// checkSend refuses a send no channel could deliver: a disabled or
// misconfigured channel, or a document past its bounds.
func checkSend(s ChannelSend) error {
	if !s.Channel.Enabled {
		return fmt.Errorf("channel %q is disabled", s.Channel.Name)
	}
	if err := ValidateChannel(s.Channel); err != nil {
		return fmt.Errorf("channel %q is misconfigured: %w", s.Channel.Name, err)
	}
	return ValidateDocument(s.Document)
}

// gateFor builds the gate a send is checked against. A daily channel's rows
// are not keyed: they are already one bulletin a day, which is the quiet a key
// exists to buy.
func gateFor(s ChannelSend) ChannelGate {
	gate := ChannelGate{Channel: s.Channel.Name, MaxPerHour: s.Channel.HourlyCap()}
	if s.Document.Key == "" || s.Channel.Mode == ChannelModeDaily {
		return gate
	}
	gate.Key = s.Document.Key
	gate.Window = max(s.Channel.RepeatWindow(), s.RepeatAfter)
	return gate
}

// channelRows builds the rows one send writes. It consults no preference
// store for an HTTP kind: the recipient is a destination rather than a person,
// so there is nobody whose preferences could apply.
func (e *Enqueuer) channelRows(ctx context.Context, s ChannelSend) ([]Notification, error) {
	ch := s.Channel
	payload := channelPayload(s.Actor, s.Document)
	if ch.Kind != ChannelKindEmail {
		n := Notification{Recipient: ch.Recipient(), Category: CategoryChannel, Channel: ch.Name, Payload: payload}
		e.applyChannelSchedule(&n, ch, false)
		return []Notification{n}, nil
	}
	rows := make([]Notification, 0, len(ch.Recipients))
	for _, addr := range ch.Recipients {
		recipient := NormalizeAddress(addr)
		if recipient == "" || !Deliverable(recipient) {
			continue
		}
		prefs, err := e.prefs.Get(ctx, recipient)
		if err != nil {
			return nil, fmt.Errorf("reading notification prefs for %s: %w", logsan.SanitizeForLog(recipient), err)
		}
		if prefs.Mode == ModeOff {
			continue
		}
		n := Notification{Recipient: recipient, Category: CategoryChannel, Channel: ch.Name, Payload: payload}
		e.applyChannelSchedule(&n, ch, prefs.Mode == ModeDaily)
		rows = append(rows, n)
	}
	return rows, nil
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
