package notifydelivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// defaultPrefs implements notification.PrefsStore returning defaults.
type defaultPrefs struct{}

func (defaultPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

func (defaultPrefs) Set(_ context.Context, email string, _ notification.PrefsUpdate) (notification.Prefs, error) {
	return notification.DefaultPrefs(email), nil
}

// recordQueue implements notification.QueueStore recording enqueues.
type recordQueue struct {
	mu   sync.Mutex
	rows []notification.Notification
}

func (q *recordQueue) Enqueue(_ context.Context, n notification.Notification) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows = append(q.rows, n)
	return nil
}

func (*recordQueue) ClaimImmediate(context.Context, time.Duration) (*notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*recordQueue) ClaimDigest(context.Context, time.Duration) ([]notification.Notification, error) {
	return nil, notification.ErrNoWork
}

func (*recordQueue) MarkSent(context.Context, []int64) error                     { return nil }
func (*recordQueue) Retry(context.Context, []int64, string, time.Duration) error { return nil }
func (*recordQueue) Fail(context.Context, []int64, string) error                 { return nil }
func (*recordQueue) PurgeOld(context.Context, time.Duration, time.Duration) (int64, error) {
	return 0, nil
}

func (q *recordQueue) snapshot() []notification.Notification {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]notification.Notification(nil), q.rows...)
}

// Store fakes embed the interface for method-set completeness and override
// only the getters the bridge calls.
type fakeAssets struct {
	portal.AssetStore
	asset *portal.Asset
	err   error
}

func (f *fakeAssets) Get(context.Context, string) (*portal.Asset, error) { return f.asset, f.err }

type fakeCollections struct {
	portal.CollectionStore
	coll *portal.Collection
	err  error
}

func (f *fakeCollections) Get(context.Context, string) (*portal.Collection, error) {
	return f.coll, f.err
}

type fakePrompts struct {
	portal.PromptStore
	prompt *prompt.Prompt
	err    error
}

func (f *fakePrompts) GetByID(context.Context, string) (*prompt.Prompt, error) {
	return f.prompt, f.err
}

type fakePages struct {
	knowledgepage.Store
	page *knowledgepage.Page
	err  error
}

func (f *fakePages) Get(context.Context, string) (*knowledgepage.Page, error) {
	return f.page, f.err
}

func bridgeUnderTest(t *testing.T, stores PortalStores, baseURL string) (*PortalNotifier, *recordQueue) {
	t.Helper()
	return bridgeWithPrefs(t, stores, baseURL, defaultPrefs{})
}

// bridgeWithPrefs is bridgeUnderTest over a chosen preference store, for the
// cases where what a recipient has muted decides the outcome.
func bridgeWithPrefs(t *testing.T, stores PortalStores, baseURL string, prefs notification.PrefsStore) (*PortalNotifier, *recordQueue) {
	t.Helper()
	queue := &recordQueue{}
	enq := notification.NewEnqueuer(prefs, queue, 13)
	t.Cleanup(enq.Close)
	return NewPortalNotifier(enq, stores, baseURL), queue
}

// mentionsOffPrefs mutes the mention category and leaves every other default.
type mentionsOffPrefs struct{ defaultPrefs }

func (mentionsOffPrefs) Get(_ context.Context, email string) (notification.Prefs, error) {
	p := notification.DefaultPrefs(email)
	p.MentionsEnabled = false
	return p, nil
}

func TestNotifyShare_TokenViewerLink(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{}, "https://x.io")

	n.NotifyShare(context.Background(),
		&portal.Share{Token: "tok", CreatedBy: "o@b.io", SharedWithEmail: "r@b.io"},
		"asset", "a1", "Report")

	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Payload.Link != "https://x.io/portal/view/tok" {
		t.Errorf("link = %q", rows[0].Payload.Link)
	}
	if rows[0].Recipient != "r@b.io" || rows[0].Payload.ItemTitle != "Report" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

func TestNotifyShare_PromptLinksToPromptPage(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{}, "https://x.io")

	n.NotifyShare(context.Background(),
		&portal.Share{Token: "tok", CreatedBy: "o@b.io", SharedWithEmail: "r@b.io"},
		"prompt", "pr1", "Daily Report")

	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Payload.Link != "https://x.io/portal/prompts/pr1" {
		t.Errorf("prompt link = %q", rows[0].Payload.Link)
	}
}

func TestNotifyShare_TokenOnlyShareQueuesNothing(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{}, "https://x.io")

	n.NotifyShare(context.Background(),
		&portal.Share{Token: "tok", CreatedBy: "o@b.io"}, "asset", "a1", "Report")

	if len(queue.snapshot()) != 0 {
		t.Error("share without a recipient email must queue nothing")
	}
}

func TestNotifyShare_DefaultNoticeSuppressed(t *testing.T) {
	n, queue := bridgeUnderTest(t, PortalStores{}, "https://x.io")

	n.NotifyShare(context.Background(), &portal.Share{
		Token: "t1", CreatedBy: "o@b.io", SharedWithEmail: "r@b.io",
		NoticeText: portal.DefaultNoticeText,
	}, "asset", "a1", "Report")
	n.NotifyShare(context.Background(), &portal.Share{
		Token: "t2", CreatedBy: "o@b.io", SharedWithEmail: "r2@b.io",
		NoticeText: "Please review by Friday",
	}, "asset", "a1", "Report")

	rows := queue.snapshot()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Payload.Message != "" {
		t.Errorf("default notice must not render as a personal message: %q", rows[0].Payload.Message)
	}
	if rows[1].Payload.Message != "Please review by Friday" {
		t.Errorf("custom notice must pass through: %q", rows[1].Payload.Message)
	}
}

func TestNotifyThreadEvent_AssetOwnerNotified(t *testing.T) {
	stores := PortalStores{Assets: &fakeAssets{
		asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"},
	}}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "Nice work", nil)

	rows := queue.snapshot()
	if len(rows) != 2 {
		t.Fatalf("expected owner + thread author, got %d rows", len(rows))
	}
	recipients := map[string]bool{}
	for _, r := range rows {
		recipients[r.Recipient] = true
		if r.Category != notification.CategoryComment || r.Payload.Kind != notification.KindComment {
			t.Errorf("category/kind wrong: %+v", r)
		}
		if r.Payload.Link != "https://x.io/portal/assets/a1" || r.Payload.ItemTitle != "Report" {
			t.Errorf("target wrong: %+v", r.Payload)
		}
	}
	if !recipients["owner@b.io"] || !recipients["author@b.io"] {
		t.Errorf("recipients = %v", recipients)
	}
}

func TestNotifyThreadEvent_FeedbackKind(t *testing.T) {
	stores := PortalStores{Assets: &fakeAssets{
		asset: &portal.Asset{ID: "a1", Name: "R", OwnerEmail: "owner@b.io"},
	}}
	n, queue := bridgeUnderTest(t, stores, "")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindRating, AssetID: "a1", AuthorEmail: "owner@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "rater@b.io", "4 stars", nil)

	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Payload.Kind != notification.KindFeedback {
		t.Errorf("kind = %q; want feedback", rows[0].Payload.Kind)
	}
}

func TestNotifyThreadEvent_CollectionOwnerNotified(t *testing.T) {
	stores := PortalStores{Collections: &fakeCollections{
		coll: &portal.Collection{ID: "c1", Name: "Q3 Pack", OwnerEmail: "owner@b.io"},
	}}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, CollectionID: "c1", AuthorEmail: "owner@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "hi", nil)

	rows := queue.snapshot()
	if len(rows) != 1 || rows[0].Payload.Link != "https://x.io/portal/collections/c1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestNotifyThreadEvent_PromptOwnerNotified(t *testing.T) {
	stores := PortalStores{Prompts: &fakePrompts{
		prompt: &prompt.Prompt{ID: "p1", DisplayName: "Daily", OwnerEmail: "owner@b.io"},
	}}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, PromptID: "p1", AuthorEmail: "owner@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "hi", nil)

	rows := queue.snapshot()
	if len(rows) != 1 || rows[0].Payload.Link != "https://x.io/portal/prompts/p1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestNotifyThreadEvent_KnowledgePageOwnerNotified(t *testing.T) {
	stores := PortalStores{KnowledgePages: &fakePages{
		page: &knowledgepage.Page{ID: "kp1", Title: "Fiscal Calendar", CreatedEmail: "owner@b.io"},
	}}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, KnowledgePageID: "kp1", AuthorEmail: "owner@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "clarify Q3", nil)

	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected the page owner to be notified, got %d rows", len(rows))
	}
	if rows[0].Recipient != "owner@b.io" || rows[0].Payload.ItemTitle != "Fiscal Calendar" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
	if rows[0].Payload.Link != "https://x.io/portal/knowledge/pages/kp1" {
		t.Errorf("link = %q", rows[0].Payload.Link)
	}
}

func TestNotifyThreadEvent_UnresolvableTargets(t *testing.T) {
	storeErr := context.DeadlineExceeded
	stores := PortalStores{
		Assets:      &fakeAssets{err: storeErr},
		Collections: &fakeCollections{err: storeErr},
		Prompts:     &fakePrompts{}, // GetByID returns nil, nil
		// KnowledgePages nil: the branch must not panic.
	}
	tests := []struct {
		name   string
		thread *portal.Thread
	}{
		{name: "asset store error", thread: &portal.Thread{AssetID: "a1"}},
		{name: "collection store error", thread: &portal.Thread{CollectionID: "c1"}},
		{name: "prompt missing", thread: &portal.Thread{PromptID: "p1"}},
		{name: "knowledge page store nil", thread: &portal.Thread{KnowledgePageID: "kp1"}},
		{name: "standalone", thread: &portal.Thread{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, queue := bridgeUnderTest(t, stores, "https://x.io")
			tc.thread.Kind = portal.ThreadKindComment
			tc.thread.Title = "fallback"
			tc.thread.AuthorEmail = "author@b.io"
			n.NotifyThreadEvent(context.Background(), tc.thread, "commenter@b.io", "hi", nil)

			rows := queue.snapshot()
			if len(rows) != 1 {
				t.Fatalf("thread author must still be notified, got %d rows", len(rows))
			}
			if rows[0].Payload.ItemTitle != "fallback" || rows[0].Payload.Link != "" {
				t.Errorf("unresolvable target must fall back: %+v", rows[0].Payload)
			}
		})
	}
}

func TestHandlePortalNotifier_NilHandle(t *testing.T) {
	var h *Handle
	if h.PortalNotifier(PortalStores{}, "") != nil {
		t.Error("nil handle must yield nil notifier")
	}
}

func TestHandlePortalNotifier_LiveHandle(t *testing.T) {
	h := newTestHandle(t, nil)
	if h.PortalNotifier(PortalStores{}, "https://x.io") == nil {
		t.Error("live handle must yield a notifier")
	}
}

// fakeGrantees answers the "who is this shared with" lookup.
type fakeGrantees struct {
	emails []string
	err    error
	gotID  string
}

func (f *fakeGrantees) Grantees(_ context.Context, _, targetID string) ([]string, error) {
	f.gotID = targetID
	return f.emails, f.err
}

// A comment must reach the people the item is shared with, not just its owner
// and the thread author: that gap is why sharing an asset and commenting on it
// notified nobody (#627).
func TestNotifyThreadEvent_ShareRecipientsNotified(t *testing.T) {
	grants := &fakeGrantees{emails: []string{"owner@b.io", "teammate@b.io"}}
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: grants,
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "owner@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "owner@b.io", "Take a look", nil)

	if grants.gotID != "a1" {
		t.Errorf("grantee lookup ran against %q, want the thread's asset", grants.gotID)
	}
	rows := queue.snapshot()
	if len(rows) != 1 || rows[0].Recipient != "teammate@b.io" {
		t.Fatalf("expected the share recipient to be notified, got %+v", rows)
	}
	if rows[0].Category != notification.CategoryComment {
		t.Errorf("category wrong: %+v", rows[0])
	}
}

func TestNotifyThreadEvent_MentionedGetTheirOwnCategoryAndNoDuplicate(t *testing.T) {
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: &fakeGrantees{emails: []string{"owner@b.io", "named@b.io", "bystander@b.io"}},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "@named(b.io) thoughts?",
		[]string{"named@b.io"})

	byRecipient := map[string]notification.Notification{}
	for _, r := range queue.snapshot() {
		if prev, dup := byRecipient[r.Recipient]; dup {
			t.Fatalf("%s notified twice: %+v and %+v", r.Recipient, prev, r)
		}
		byRecipient[r.Recipient] = r
	}
	named, ok := byRecipient["named@b.io"]
	if !ok {
		t.Fatal("the mentioned person was not notified")
	}
	if named.Category != notification.CategoryMention || named.Payload.Kind != notification.KindMention {
		t.Errorf("mention must use its own category and kind: %+v", named)
	}
	for _, other := range []string{"owner@b.io", "author@b.io", "bystander@b.io"} {
		if got, ok := byRecipient[other]; !ok {
			t.Errorf("%s was not notified", other)
		} else if got.Category != notification.CategoryComment {
			t.Errorf("%s must get the plain comment notification: %+v", other, got)
		}
	}
}

// Mentions are enqueued before the general fan-out, so when a comment on a
// widely-shared item exhausts the actor's rate limit, the people addressed by
// name are the ones that get through.
func TestNotifyThreadEvent_MentionsQueuedFirst(t *testing.T) {
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: &fakeGrantees{emails: []string{"owner@b.io", "named@b.io"}},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "@named(b.io) ping", []string{"named@b.io"})

	rows := queue.snapshot()
	if len(rows) == 0 || rows[0].Recipient != "named@b.io" {
		t.Fatalf("mention must be enqueued first, got %+v", rows)
	}
}

func TestNotifyThreadEvent_MentionOfTheActorIsDropped(t *testing.T) {
	stores := PortalStores{Assets: &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report"}}}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "me@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "me@b.io", "@me(b.io) note to self", []string{"me@b.io"})

	if rows := queue.snapshot(); len(rows) != 0 {
		t.Fatalf("mentioning yourself must notify nobody, got %+v", rows)
	}
}

// A grantee lookup failure must not silence the notification entirely: the
// owner and thread author still hear about the comment.
func TestNotifyThreadEvent_GranteeLookupFailureKeepsOwnerNotified(t *testing.T) {
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: &fakeGrantees{err: errors.New("database down")},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "hi", nil)

	if rows := queue.snapshot(); len(rows) != 2 {
		t.Fatalf("expected owner + author despite the lookup failure, got %+v", rows)
	}
}

func TestExcluding(t *testing.T) {
	got := excluding([]string{"A@b.io", "c@b.io"}, []string{"a@b.io"})
	if len(got) != 1 || got[0] != "c@b.io" {
		t.Fatalf("excluding must drop case-insensitively: %+v", got)
	}
	if got := excluding([]string{"a@b.io"}, nil); len(got) != 1 {
		t.Fatalf("no exclusions must leave the list alone: %+v", got)
	}
}

// Muting mentions must not cost someone the comment email they would have had
// without being named: the general fan-out only skips a person whose mention
// was actually queued.
func TestNotifyThreadEvent_MutedMentionStillGetsTheCommentEmail(t *testing.T) {
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: &fakeGrantees{emails: []string{"owner@b.io"}},
	}
	n, queue := bridgeWithPrefs(t, stores, "https://x.io", mentionsOffPrefs{})

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "@owner(b.io) look", []string{"owner@b.io"})

	rows := queue.snapshot()
	if len(rows) != 2 {
		t.Fatalf("expected the owner and the thread author to be notified, got %+v", rows)
	}
	for _, r := range rows {
		if r.Category != notification.CategoryComment {
			t.Errorf("%s must fall back to the comment category: %+v", r.Recipient, r)
		}
	}
}

// The size of a target's share list is a property of the item, not a choice
// the author made, so fanning out to it must not spend the budget that bounds
// the addresses the author does choose.
func TestNotifyThreadEvent_FanoutDoesNotExhaustTheActorBudget(t *testing.T) {
	many := make([]string, 0, 40)
	for i := range 40 {
		many = append(many, fmt.Sprintf("teammate%02d@b.io", i))
	}
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: &fakeGrantees{emails: many},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io"}
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "heads up", nil)
	if got := len(queue.snapshot()); got != len(many)+2 {
		t.Fatalf("every grantee, the owner, and the author must be notified: got %d of %d", got, len(many)+2)
	}

	// The same actor's next share still goes out: the fan-out cost one token,
	// not one per recipient.
	n.NotifyShare(context.Background(),
		&portal.Share{Token: "tok", CreatedBy: "commenter@b.io", SharedWithEmail: "later@b.io"},
		notification.KindAsset, "a2", "Another")

	var shared bool
	for _, r := range queue.snapshot() {
		if r.Recipient == "later@b.io" && r.Category == notification.CategoryShare {
			shared = true
		}
	}
	if !shared {
		t.Fatal("the share notification was dropped: the comment fan-out spent the actor's rate limit")
	}
}

// Commenting on an item you own must notify you of nothing, whatever address
// shape the target's owner row happens to hold. Comparing the raw strings let
// "Display Name <addr>" survive the actor exclusion and mail the author their
// own comment (#1100).
func TestNotifyThreadEvent_OwnerInDisplayFormIsStillTheActor(t *testing.T) {
	stores := PortalStores{
		Assets: &fakeAssets{asset: &portal.Asset{
			ID: "a1", Name: "Report", OwnerEmail: "Owner Person <owner@b.io>",
		}},
		Grantees: &fakeGrantees{emails: []string{"Owner Person <OWNER@b.io>", "teammate@b.io"}},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{
		ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1",
		AuthorEmail: "Owner Person <owner@b.io>",
	}
	n.NotifyThreadEvent(context.Background(), thread, "owner@b.io", "note to self", nil)

	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected only the other grantee to be notified, got %+v", rows)
	}
	if rows[0].Recipient != "teammate@b.io" {
		t.Errorf("Recipient = %q; the author must not be notified of their own comment", rows[0].Recipient)
	}
}

// Naming yourself in your own comment queues nothing: not the mention, and
// not the comment notification the fan-out would otherwise owe the owner.
func TestNotifyThreadEvent_SelfMentionInDisplayFormQueuesNothing(t *testing.T) {
	stores := PortalStores{
		Assets: &fakeAssets{asset: &portal.Asset{
			ID: "a1", Name: "Report", OwnerEmail: "Me Myself <me@b.io>",
		}},
		Grantees: &fakeGrantees{emails: []string{"Me Myself <me@b.io>"}},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{
		ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "me@b.io",
	}
	n.NotifyThreadEvent(context.Background(), thread, "me@b.io", "@me(b.io) remember this",
		[]string{"Me Myself <me@b.io>"})

	if rows := queue.snapshot(); len(rows) != 0 {
		t.Fatalf("mentioning yourself on your own item must notify nobody, got %+v", rows)
	}
}

// An event whose author cannot be resolved is not provably someone else's, and
// the owner is always in the fan-out set, so the fan-out is dropped. Mentions
// still go out: the body named those addresses explicitly.
func TestNotifyThreadEvent_ActorlessEventSkipsFanoutKeepsMentions(t *testing.T) {
	stores := PortalStores{
		Assets:   &fakeAssets{asset: &portal.Asset{ID: "a1", Name: "Report", OwnerEmail: "owner@b.io"}},
		Grantees: &fakeGrantees{emails: []string{"owner@b.io", "teammate@b.io"}},
	}
	n, queue := bridgeUnderTest(t, stores, "https://x.io")

	thread := &portal.Thread{
		ID: "th1", Kind: portal.ThreadKindComment, AssetID: "a1", AuthorEmail: "author@b.io",
	}
	n.NotifyThreadEvent(context.Background(), thread, "  ", "@named(b.io) look", []string{"named@b.io"})

	rows := queue.snapshot()
	if len(rows) != 1 {
		t.Fatalf("expected only the explicit mention, got %+v", rows)
	}
	if rows[0].Recipient != "named@b.io" || rows[0].Category != notification.CategoryMention {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}
