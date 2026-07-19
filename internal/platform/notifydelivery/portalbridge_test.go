package notifydelivery

import (
	"context"
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
	queue := &recordQueue{}
	enq := notification.NewEnqueuer(defaultPrefs{}, queue, 13)
	t.Cleanup(enq.Close)
	return NewPortalNotifier(enq, stores, baseURL), queue
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
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "Nice work")

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
	n.NotifyThreadEvent(context.Background(), thread, "rater@b.io", "4 stars")

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
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "hi")

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
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "hi")

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
	n.NotifyThreadEvent(context.Background(), thread, "commenter@b.io", "clarify Q3")

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
			n.NotifyThreadEvent(context.Background(), tc.thread, "commenter@b.io", "hi")

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
