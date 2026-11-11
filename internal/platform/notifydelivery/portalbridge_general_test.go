package notifydelivery

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// fakeGeneral is a General feedback list.
type fakeGeneral struct {
	people, channels []string
	acknowledge      bool
}

func (f fakeGeneral) Recipients(context.Context) (people, channels []string, acknowledge bool) {
	return f.people, f.channels, f.acknowledge
}

// recordChannels records each channel send and reports each name queued.
type recordChannels struct {
	mu    sync.Mutex
	sends []channelSend
}

type channelSend struct {
	names   []string
	key     string
	payload notification.Payload
}

func (r *recordChannels) Send(_ context.Context, names []string, key string, p notification.Payload) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, channelSend{names: names, key: key, payload: p})
	return len(names)
}

// reachNobodyButOwners can open nothing; it stands for an item's access check.
type reachNobodyButOwners struct{ allowed []string }

func (r reachNobodyButOwners) CanOpen(_ context.Context, email, _, _ string) bool {
	return slices.Contains(r.allowed, email)
}

func recipientsOfKind(rows []notification.Notification, kind string) []string {
	var out []string
	for _, r := range rows {
		if r.Payload.Kind == kind {
			out = append(out, r.Recipient)
		}
	}
	slices.Sort(out)
	return out
}

func generalThread() *portal.Thread {
	return &portal.Thread{ID: "th1", TargetType: "standalone", Kind: threads.ThreadKindSuggestion, Title: "Need the Q3 extract", AuthorEmail: "jordan@b.io"}
}

// TestGeneral_ANewPostReachesTheList pins #2603: a new General thread with no
// mention notifies the list's people (an address the directory does not hold
// included, the poster excluded), links to the thread on the Feedback page,
// is sent to the list's channels once, and is acknowledged to its author.
func TestGeneral_ANewPostReachesTheList(t *testing.T) {
	channels := &recordChannels{}
	n, queue := bridgeUnderTest(t, PortalStores{
		General:  fakeGeneral{people: []string{"admin@b.io", "data-requests@b.io", "jordan@b.io"}, channels: []string{"data-team"}, acknowledge: true},
		Channels: channels,
		Reach:    reachNobodyButOwners{allowed: []string{"admin@b.io"}},
	}, "https://x.io")

	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: generalThread(), EventID: "e1", ActorEmail: "jordan@b.io", Body: "Could someone pull it?", Opened: true,
	})

	rows := queue.snapshot()
	if got := recipientsOfKind(rows, notification.KindFeedback); !slices.Equal(got, []string{"admin@b.io", "data-requests@b.io"}) {
		t.Errorf("told %v; want the list's people, the poster excluded, an address the directory lacks included", got)
	}
	for _, r := range rows {
		if r.Payload.Kind == notification.KindFeedback && r.Payload.Link != "https://x.io/portal/feedback#thread-th1" {
			t.Errorf("link = %q", r.Payload.Link)
		}
	}
	if len(channels.sends) != 1 || channels.sends[0].key != "general_feedback:th1" || !slices.Equal(channels.sends[0].names, []string{"data-team"}) {
		t.Errorf("channel sends = %+v", channels.sends)
	}
	ack := recipientsOfKind(rows, notification.KindFeedbackReceived)
	if !slices.Equal(ack, []string{"jordan@b.io"}) {
		t.Errorf("acknowledged %v; want the poster", ack)
	}
}

// A reply on a General thread notifies the list and the author, the actor
// excluded, and goes to no channel and acknowledges nothing.
func TestGeneral_AReplyNotifiesTheListAndTheAuthor(t *testing.T) {
	channels := &recordChannels{}
	n, queue := bridgeUnderTest(t, PortalStores{
		General:  fakeGeneral{people: []string{"admin@b.io"}, channels: []string{"data-team"}, acknowledge: true},
		Channels: channels,
	}, "https://x.io")

	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: generalThread(), EventID: "e2", ActorEmail: "admin@b.io", Body: "On it.",
	})

	if got := recipientsOfKind(queue.snapshot(), notification.KindFeedback); !slices.Equal(got, []string{"jordan@b.io"}) {
		t.Errorf("told %v; want the author, the replying admin excluded", got)
	}
	if len(channels.sends) != 0 {
		t.Errorf("a reply went to channels: %+v", channels.sends)
	}
	if got := recipientsOfKind(queue.snapshot(), notification.KindFeedbackReceived); len(got) != 0 {
		t.Errorf("a reply was acknowledged to %v", got)
	}
}

// A list that reaches nobody acknowledges nothing: the author is never told a
// post went somewhere it did not.
func TestGeneral_AListThatReachesNobodyAcknowledgesNothing(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{General: fakeGeneral{acknowledge: true}, Channels: &recordChannels{}}, "https://x.io")
	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: generalThread(), EventID: "e3", ActorEmail: "jordan@b.io", Body: "hello", Opened: true,
	})
	if rows := queue.snapshot(); len(rows) != 0 {
		t.Errorf("queued %+v for a list that reaches nobody", rows)
	}
}

// With acknowledgement off, the list is told and the author is not.
func TestGeneral_AcknowledgementOff(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{General: fakeGeneral{people: []string{"admin@b.io"}}}, "https://x.io")
	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: generalThread(), EventID: "e4", ActorEmail: "jordan@b.io", Body: "hello", Opened: true,
	})
	if got := recipientsOfKind(queue.snapshot(), notification.KindFeedbackReceived); len(got) != 0 {
		t.Errorf("acknowledged %v with acknowledgement off", got)
	}
}

// An internal General thread tells no external account on the list (#2227).
func TestGeneral_AnInternalThreadSkipsExternalAccounts(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{
		General:  fakeGeneral{people: []string{"admin@b.io", "vendor@ext.io"}},
		External: func(_ context.Context, email string) bool { return email == "vendor@ext.io" },
	}, "https://x.io")
	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{
		Thread: generalThread(), EventID: "e5", ActorEmail: "jordan@b.io", Body: "hello", Opened: true,
	})
	if got := recipientsOfKind(queue.snapshot(), notification.KindFeedback); !slices.Equal(got, []string{"admin@b.io"}) {
		t.Errorf("told %v; an internal thread reaches no external account", got)
	}
}

// A thread on an item whose owner cannot be found falls back to the list,
// each person still held to the item's access; a thread on an item with an
// owner takes nothing from the list.
func TestGeneral_AnOwnerlessItemFallsBackToTheListUnderItsAccess(t *testing.T) {
	general := fakeGeneral{people: []string{"admin@b.io", "outsider@b.io"}}
	reach := reachNobodyButOwners{allowed: []string{"admin@b.io", "owner@b.io", "author@b.io"}}

	ownerless := PortalStores{Assets: &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Orphan"}}, General: general, Reach: reach}
	n, queue := bridgeUnderTest(t, ownerless, "https://x.io")
	n.NotifyThreadEvent(context.Background(), &portal.Thread{AssetID: "a1", TargetType: "asset", Kind: portal.ThreadKindComment, AuthorEmail: "author@b.io"}, "author@b.io", "hi", nil)
	if got := recipientsOfKind(queue.snapshot(), notification.KindComment); !slices.Equal(got, []string{"admin@b.io"}) {
		t.Errorf("an ownerless item told %v; want the list's people who can open it", got)
	}

	owned := PortalStores{Assets: &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}}, General: general, Reach: reach}
	n, queue = bridgeUnderTest(t, owned, "https://x.io")
	n.NotifyThreadEvent(context.Background(), &portal.Thread{AssetID: "a1", TargetType: "asset", Kind: portal.ThreadKindComment, AuthorEmail: "author@b.io"}, "author@b.io", "hi", nil)
	if got := recipientsOfKind(queue.snapshot(), notification.KindComment); !slices.Equal(got, []string{"owner@b.io"}) {
		t.Errorf("an owned item told %v; want its owner only", got)
	}
}

// fakeEvents is a thread's stored messages.
type fakeEvents struct {
	events []threads.ThreadEvent
	err    error
}

func (f fakeEvents) ListEvents(context.Context, string) ([]threads.ThreadEvent, error) {
	return f.events, f.err
}

// A General thread has no item and, since #2513, no stored title: its
// notifications are named by the first line of its first message with text,
// from the opening message itself or from the store on a reply, and
// generically when neither is at hand.
func TestGeneral_AThreadIsNamedByItsFirstLine(t *testing.T) {
	stored := fakeEvents{events: []threads.ThreadEvent{{Body: ""}, {Body: "\nNeed the Q3 extract\nby region"}, {Body: "On it."}}}
	for _, tc := range []struct {
		name   string
		events ThreadEvents
		msg    threads.MessageNotice
		want   string
	}{
		{"the opening message", nil, threads.MessageNotice{Body: "\nNeed the Q3 extract\nby region", Opened: true}, "Need the Q3 extract"},
		{"a reply, from the store", stored, threads.MessageNotice{Body: "On it."}, "Need the Q3 extract"},
		{"a reply with no store", nil, threads.MessageNotice{Body: "On it."}, generalHeading},
		{"a reply the store cannot read", fakeEvents{err: errors.New("down")}, threads.MessageNotice{Body: "On it."}, generalHeading},
		{"an opening message with no text", nil, threads.MessageNotice{Body: "  ", Opened: true}, generalHeading},
	} {
		n, queue := bridgeUnderTest(t, PortalStores{General: fakeGeneral{people: []string{"admin@b.io"}}, Events: tc.events}, "https://x.io")
		th := generalThread()
		th.Title = ""
		tc.msg.Thread, tc.msg.EventID, tc.msg.ActorEmail = th, "e1", "jordan@b.io"
		n.NotifyThreadMessage(context.Background(), tc.msg)
		rows := queue.snapshot()
		if len(rows) == 0 || rows[0].Payload.ItemTitle != tc.want {
			t.Errorf("%s: rows %+v; want item title %q", tc.name, rows, tc.want)
		}
	}
}

// The author of a General thread hears of a reply though the directory does
// not hold them (they posted with an API key); on an item, the author is held
// to the item's access like anyone else.
func TestGeneral_TheAuthorHearsOfAReplyOutsideTheDirectory(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{General: fakeGeneral{people: []string{"admin@b.io"}}, Reach: reachNobodyButOwners{}}, "https://x.io")
	n.NotifyThreadMessage(context.Background(), threads.MessageNotice{Thread: generalThread(), EventID: "e1", ActorEmail: "admin@b.io", Body: "On it."})
	if got := recipientsOfKind(queue.snapshot(), notification.KindFeedback); !slices.Equal(got, []string{"jordan@b.io"}) {
		t.Errorf("told %v; want the General thread's author", got)
	}

	n, queue = bridgeUnderTest(t, PortalStores{
		Assets: &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Reach:  reachNobodyButOwners{allowed: []string{"owner@b.io"}},
	}, "https://x.io")
	n.NotifyThreadEvent(context.Background(), &portal.Thread{AssetID: "a1", TargetType: "asset", Kind: portal.ThreadKindComment, AuthorEmail: "gone@b.io"}, "owner@b.io", "hi", nil)
	if got := recipientsOfKind(queue.snapshot(), notification.KindComment); len(got) != 0 {
		t.Errorf("an item thread's author who lost access was told: %v", got)
	}
}
