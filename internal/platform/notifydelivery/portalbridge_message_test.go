package notifydelivery

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// Who one message tells (#2512): the thread's participants join the owner and
// the grantees, everyone must be able to open the target now, and each row
// names the message so a delete can take it back (#2514).

type fakeParticipants struct {
	people []string
	err    error
}

func (f *fakeParticipants) Participants(context.Context, string) ([]string, error) {
	return f.people, f.err
}

// fakeReach admits the addresses in open.
type fakeReach map[string]bool

func (f fakeReach) CanOpen(_ context.Context, email, _, _ string) bool { return f[email] }

type fakeRetractor struct {
	got []string
	err error
}

func (f *fakeRetractor) RetractEvent(_ context.Context, id string) error {
	f.got = append(f.got, id)
	return f.err
}

func recipientsOf(rows []notification.Notification, category string) []string {
	out := []string{}
	for _, r := range rows {
		if r.Category == category {
			out = append(out, r.Recipient)
		}
	}
	sort.Strings(out)
	return out
}

func messageStores(participants []string, reach fakeReach) PortalStores {
	return PortalStores{
		Assets:       &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees:     &fakeGrantees{emails: []string{"owner@b.io", "emailshare@b.io", "revoked@b.io"}},
		Participants: &fakeParticipants{people: participants},
		Reach:        reach,
	}
}

func TestNotifyThreadMessage_ParticipantsAreNotified(t *testing.T) {
	reach := fakeReach{"owner@b.io": true, "emailshare@b.io": true, "author@b.io": true, "replier@b.io": true, "named@b.io": true}
	n, queue := bridgeUnderTest(t, messageStores([]string{"author@b.io", "replier@b.io", "named@b.io", "lost@b.io"}, reach), "")
	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, TargetType: "asset", AssetID: "a1", AuthorEmail: "author@b.io"}

	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: thread, EventID: "evt_9", ActorEmail: "owner@b.io", Body: "Fixed it",
	})

	got := recipientsOf(queue.snapshot(), notification.CategoryComment)
	want := []string{"author@b.io", "emailshare@b.io", "named@b.io", "replier@b.io"}
	if len(got) != len(want) {
		t.Fatalf("recipients = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recipients = %v; want %v (the author never hears of their own message; "+
				"people who cannot open the target hear nothing)", got, want)
		}
	}
	for _, r := range queue.snapshot() {
		if r.Payload.EventID != "evt_9" {
			t.Errorf("row %s does not name the message: %+v", r.Recipient, r.Payload)
		}
	}
}

func TestNotifyThreadMessage_MentionsOnly(t *testing.T) {
	reach := fakeReach{"owner@b.io": true, "author@b.io": true, "new@b.io": true}
	n, queue := bridgeUnderTest(t, messageStores([]string{"author@b.io"}, reach), "")
	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, TargetType: "asset", AssetID: "a1", AuthorEmail: "author@b.io"}

	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: thread, EventID: "evt_1", ActorEmail: "author@b.io", Body: "@new(b.io)",
		Mentioned: []string{"new@b.io"}, MentionsOnly: true,
	})

	rows := queue.snapshot()
	if len(rows) != 1 || rows[0].Recipient != "new@b.io" || rows[0].Category != notification.CategoryMention {
		t.Fatalf("an edit naming someone tells that person only; got %+v", rows)
	}
}

func TestNotifyThreadMessage_ParticipantLookupFailureKeepsTheRest(t *testing.T) {
	stores := messageStores(nil, nil)
	stores.Reach = nil
	stores.Participants = &fakeParticipants{err: errors.New("boom")}
	n, queue := bridgeUnderTest(t, stores, "")
	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, TargetType: "asset", AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{Thread: thread, ActorEmail: "owner@b.io", Body: "x"})
	if got := recipientsOf(queue.snapshot(), notification.CategoryComment); len(got) != 3 {
		t.Errorf("with no reach wired and the participant lookup failing, the author and grantees are still told: %v", got)
	}
}

func TestRetractThreadMessage(t *testing.T) {
	n, _ := bridgeUnderTest(t, PortalStores{}, "")
	n.RetractThreadMessage(context.Background(), "evt_1") // no retractor: nothing to do

	r := &fakeRetractor{}
	n.retract = r
	n.RetractThreadMessage(context.Background(), "evt_1")
	if len(r.got) != 1 || r.got[0] != "evt_1" {
		t.Errorf("retracted %v", r.got)
	}
	r.err = errors.New("boom")
	n.RetractThreadMessage(context.Background(), "evt_2") // logged, never fails the delete
}

func TestHandlePortalNotifier_CarriesTheRetractor(t *testing.T) {
	h := &Handle{retract: &fakeRetractor{}}
	if n := h.PortalNotifier(PortalStores{}, ""); n.retract == nil {
		t.Error("the handle's queue retracts a deleted message")
	}
	if (*Handle)(nil).Inbox() != nil {
		t.Error("a nil handle has no inbox")
	}
	if (&Handle{inbox: fakeInbox{}}).Inbox() == nil {
		t.Error("a live handle hands out its inbox")
	}
}

type fakeInbox struct{ notification.InboxStore }
