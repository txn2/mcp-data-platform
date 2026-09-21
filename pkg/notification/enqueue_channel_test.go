package notification

import (
	"context"
	"strings"
	"testing"
	"time"
)

// channelEnqueuer builds an Enqueuer over the package's existing fakes, with
// a fixed clock so a digest row's schedule is assertable.
func channelEnqueuer(t *testing.T, prefs map[string]Prefs) (*Enqueuer, *fakeQueueStore) {
	t.Helper()
	queue := &fakeQueueStore{}
	e := NewEnqueuer(&fakePrefsStore{prefs: prefs}, queue, 13)
	e.now = func() time.Time { return time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC) }
	t.Cleanup(e.Close)
	return e, queue
}

func TestNotifyChannel_HTTPKindWritesOneRowAddressedToTheChannel(t *testing.T) {
	e, queue := channelEnqueuer(t, nil)
	ch := Channel{
		Name: "ops", Kind: ChannelKindMattermost, Enabled: true, Mode: ChannelModeImmediate,
		Connection: "chat-bot", Target: "C1",
	}
	doc := Document{Title: "Threshold crossed", Body: "queue depth 812", Link: "https://x.example.com/r/1"}

	queued, err := e.NotifyChannel(context.Background(), ch, "alice@example.com", doc)
	if err != nil {
		t.Fatalf("NotifyChannel: %v", err)
	}
	if queued != 1 {
		t.Fatalf("queued = %d, want 1", queued)
	}
	rows := queue.enqueued
	if len(rows) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(rows))
	}
	n := rows[0]
	if n.Recipient != "channel:ops" {
		t.Errorf("Recipient = %q, want channel:ops", n.Recipient)
	}
	if n.Channel != "ops" {
		t.Errorf("Channel = %q, want ops; the worker claims on this column", n.Channel)
	}
	if n.Category != CategoryChannel || n.Payload.Kind != KindChannel {
		t.Errorf("category/kind = %q/%q, want %q/%q", n.Category, n.Payload.Kind, CategoryChannel, KindChannel)
	}
	if n.Payload.Document == nil {
		t.Fatal("the row carries no document, so the worker has nothing to post")
	}
	if n.Payload.Document.Title != doc.Title || n.Payload.Document.Body != doc.Body {
		t.Errorf("document = %+v, want the one that was sent", *n.Payload.Document)
	}
	if n.Digest {
		t.Error("an immediate channel produced a digest row")
	}
}

func TestNotifyChannel_EmailKindFansOutPerRecipient(t *testing.T) {
	// One row per person, not one row for the list: a person on an operator's
	// list keeps their own opt-out and their own unsubscribe link.
	e, queue := channelEnqueuer(t, map[string]Prefs{
		"muted@example.com": {Mode: ModeOff},
	})
	ch := Channel{
		Name: "ops-email", Kind: ChannelKindEmail, Enabled: true, Mode: ChannelModeImmediate,
		Recipients: []string{"a@example.com", "b@example.com", "muted@example.com"},
	}

	queued, err := e.NotifyChannel(context.Background(), ch, "alice@example.com", Document{Title: "Weekly"})
	if err != nil {
		t.Fatalf("NotifyChannel: %v", err)
	}
	if queued != 2 {
		t.Fatalf("queued = %d, want 2; the opted-out recipient must be skipped", queued)
	}
	for _, n := range queue.enqueued {
		if n.Recipient == "muted@example.com" {
			t.Error("a recipient who opted out was queued anyway")
		}
		if n.Channel != "ops-email" {
			t.Errorf("Channel = %q; a fanned-out row still names the channel it came from", n.Channel)
		}
		if strings.HasPrefix(n.Recipient, ChannelRecipientPrefix) {
			t.Errorf("Recipient = %q; an email channel's rows are addressed to people", n.Recipient)
		}
	}
}

func TestNotifyChannel_DailyChannelSchedulesADigest(t *testing.T) {
	e, queue := channelEnqueuer(t, nil)
	ch := Channel{
		Name: "digest", Kind: ChannelKindMattermost, Enabled: true, Mode: ChannelModeDaily,
		Connection: "mm", Target: "C1",
	}
	if _, err := e.NotifyChannel(context.Background(), ch, "a@example.com", Document{Title: "t"}); err != nil {
		t.Fatalf("NotifyChannel: %v", err)
	}
	n := queue.enqueued[0]
	if !n.Digest {
		t.Fatal("a daily channel produced an immediate row")
	}
	want := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	if !n.ScheduledFor.Equal(want) {
		t.Errorf("ScheduledFor = %v, want the deployment's digest window %v", n.ScheduledFor, want)
	}
}

func TestNotifyChannel_RecipientsOwnDailyModeStillBatches(t *testing.T) {
	// The channel is immediate, but this person asked for one message a day.
	// Either side's stated choice is enough to batch.
	e, queue := channelEnqueuer(t, map[string]Prefs{
		"daily@example.com": {Mode: ModeDaily},
	})
	ch := Channel{
		Name: "ops-email", Kind: ChannelKindEmail, Enabled: true, Mode: ChannelModeImmediate,
		Recipients: []string{"daily@example.com", "now@example.com"},
	}
	if _, err := e.NotifyChannel(context.Background(), ch, "a@example.com", Document{Title: "t"}); err != nil {
		t.Fatalf("NotifyChannel: %v", err)
	}
	for _, n := range queue.enqueued {
		wantDigest := n.Recipient == "daily@example.com"
		if n.Digest != wantDigest {
			t.Errorf("%s: Digest = %v, want %v", n.Recipient, n.Digest, wantDigest)
		}
	}
}

func TestNotifyChannel_RefusesWhatCannotBeDelivered(t *testing.T) {
	tests := []struct {
		name  string
		ch    Channel
		doc   Document
		wants string
	}{
		{
			"a disabled channel",
			Channel{Name: "ops", Kind: ChannelKindMattermost, Connection: "c", Target: "C1", Mode: ChannelModeImmediate},
			Document{Title: "t"},
			"is disabled",
		},
		{
			"a misconfigured channel",
			Channel{Name: "ops", Kind: ChannelKindMattermost, Enabled: true, Mode: ChannelModeImmediate},
			Document{Title: "t"},
			"misconfigured",
		},
		{
			"a document with no title",
			Channel{Name: "ops", Kind: ChannelKindMattermost, Enabled: true, Mode: ChannelModeImmediate, Connection: "c", Target: "C1"},
			Document{Body: "orphan"},
			"needs a title",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, queue := channelEnqueuer(t, nil)
			queued, err := e.NotifyChannel(context.Background(), tc.ch, "a@example.com", tc.doc)
			if err == nil {
				t.Fatal("the send was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not mention %q", err, tc.wants)
			}
			if queued != 0 || len(queue.enqueued) != 0 {
				t.Errorf("a refused send wrote %d rows", len(queue.enqueued))
			}
		})
	}
}

func TestNotifyChannel_ChargesTheActorLimitOncePerSend(t *testing.T) {
	// The size of an email channel's list is the operator's choice, not the
	// sender's, so a twenty-address channel must not exhaust the budget that
	// bounds the addresses a sender picks.
	e, queue := channelEnqueuer(t, nil)
	recipients := make([]string, MaxChannelRecipients)
	for i := range recipients {
		recipients[i] = string(rune('a'+i)) + "@example.com"
	}
	ch := Channel{
		Name: "big", Kind: ChannelKindEmail, Enabled: true, Mode: ChannelModeImmediate,
		Recipients: recipients,
	}
	queued, err := e.NotifyChannel(context.Background(), ch, "sender@example.com", Document{Title: "t"})
	if err != nil {
		t.Fatalf("NotifyChannel: %v", err)
	}
	if queued != MaxChannelRecipients {
		t.Fatalf("queued = %d, want %d", queued, MaxChannelRecipients)
	}
	if got := len(queue.enqueued); got != MaxChannelRecipients {
		t.Errorf("wrote %d rows, want %d", got, MaxChannelRecipients)
	}
	// A second send of the same size still goes through: one charge, not
	// twenty, so the actor is nowhere near their burst.
	if _, err := e.NotifyChannel(context.Background(), ch, "sender@example.com", Document{Title: "t2"}); err != nil {
		t.Fatalf("second NotifyChannel: %v", err)
	}
	if got := len(queue.enqueued); got != 2*MaxChannelRecipients {
		t.Errorf("after two sends: %d rows, want %d", got, 2*MaxChannelRecipients)
	}
}

func TestNotifyChannel_NilEnqueuerDropsSilently(t *testing.T) {
	// A deployment with no database wires no enqueuer; every caller of this
	// path is nil-safe by contract.
	var e *Enqueuer
	queued, err := e.NotifyChannel(context.Background(), Channel{Name: "ops"}, "a@example.com", Document{Title: "t"})
	if queued != 0 || err != nil {
		t.Errorf("NotifyChannel on a nil Enqueuer = %d, %v; want 0, nil", queued, err)
	}
}

func TestWantsCategory_ChannelIsAddressedByResponsibility(t *testing.T) {
	// A person on an operator's channel list has no per-category toggle --
	// the operator named them -- but ModeOff is still their own opt-out.
	if !wantsCategory(Prefs{Mode: ModeImmediate}, CategoryChannel) {
		t.Error("a channel notification was dropped by a default preference")
	}
	if wantsCategory(Prefs{Mode: ModeOff}, CategoryChannel) {
		t.Error("ModeOff did not stop a channel notification")
	}
}
