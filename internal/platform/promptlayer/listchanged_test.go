package promptlayer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/listchanged"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

// countingNotifier records how many times Notify was called. Satisfies
// ListChangedNotifier.
type countingNotifier struct{ n atomic.Int32 }

func (c *countingNotifier) Notify() { c.n.Add(1) }

// TestNotifyingStore_FiresOnSuccessfulWrites proves the store built by New wraps
// the backing store so every successful create/update/delete fires the bound
// notifier — the property that makes prompts/list_changed honest regardless of
// which write path (tool, admin, portal, knowledge) reached the store.
func TestNotifyingStore_FiresOnSuccessfulWrites(t *testing.T) {
	mock := newMockPromptStore()
	h := New(Config{Store: mock})
	c := &countingNotifier{}
	h.SetListChangedNotifier(c)

	ctx := context.Background()
	store := h.Store()

	if err := store.Create(ctx, &prompt.Prompt{Name: "p1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Update(ctx, &prompt.Prompt{Name: "p1"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := store.Delete(ctx, "p1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Create(ctx, &prompt.Prompt{Name: "p2"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.DeleteByID(ctx, "gen-p2"); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}

	if got := c.n.Load(); got != 5 {
		t.Errorf("notifier fired %d times, want 5 (one per successful write)", got)
	}
}

// TestNotifyingStore_NoFireOnError proves a failed write does not notify: a
// client must not be told the prompt set changed when it did not.
func TestNotifyingStore_NoFireOnError(t *testing.T) {
	mock := newMockPromptStore()
	mock.createErr = errors.New("boom")
	mock.updateErr = errors.New("boom")
	mock.deleteErr = errors.New("boom")
	h := New(Config{Store: mock})
	c := &countingNotifier{}
	h.SetListChangedNotifier(c)

	ctx := context.Background()
	store := h.Store()

	if err := store.Create(ctx, &prompt.Prompt{Name: "p1"}); err == nil {
		t.Fatal("Create: expected error")
	}
	if err := store.Update(ctx, &prompt.Prompt{Name: "p1"}); err == nil {
		t.Fatal("Update: expected error")
	}
	if err := store.Delete(ctx, "p1"); err == nil {
		t.Fatal("Delete: expected error")
	}
	if err := store.DeleteByID(ctx, "x"); err == nil {
		t.Fatal("DeleteByID: expected error")
	}

	if got := c.n.Load(); got != 0 {
		t.Errorf("notifier fired %d times on failed writes, want 0", got)
	}
}

// TestNotifyingStore_NilNotifierSafe proves a write before the notifier is bound
// (or in a no-broadcaster deployment) is a clean no-op, not a panic. Static
// prompt ingest at startup runs before SetListChangedNotifier is called.
func TestNotifyingStore_NilNotifierSafe(t *testing.T) {
	h := New(Config{Store: newMockPromptStore()})
	// No SetListChangedNotifier call.
	if err := h.Store().Create(context.Background(), &prompt.Prompt{Name: "p1"}); err != nil {
		t.Fatalf("Create must not error/panic without a bound notifier: %v", err)
	}
}

// TestNotifyingStore_EndToEndBroadcast is the integration proof: a prompt
// created through the store the Handle exposes reaches a broadcaster subscriber
// as notifications/prompts/list_changed, through the REAL assembled path
// (notifyingStore -> listchanged.Notifier -> session.MemoryBroadcaster ->
// subscription). No component's output is hand-fed to the next.
func TestNotifyingStore_EndToEndBroadcast(t *testing.T) {
	b := session.NewMemoryBroadcaster(nil)
	defer func() { _ = b.Close() }()

	h := New(Config{Store: newMockPromptStore()})
	h.SetListChangedNotifier(listchanged.New(b, "notifications/prompts/list_changed"))

	ctx := t.Context()
	sub := b.Subscribe(ctx, "session-1")
	defer sub.Close()

	if err := h.Store().Create(ctx, &prompt.Prompt{Name: "new-prompt"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	select {
	case ev, ok := <-sub.Events():
		if !ok {
			t.Fatal("subscription closed before event")
		}
		if ev.Method != "notifications/prompts/list_changed" {
			t.Errorf("event method = %q, want notifications/prompts/list_changed", ev.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompts/list_changed after prompt create")
	}
}

// TestNotifyingStore_PreservesSearchCapability proves wrapStore keeps the
// backing store's prompt.Searcher capability through the wrapper (so the
// manage_prompt / portal / searchfed up-casts still succeed) while still firing
// the notifier on writes. A store that is NOT a Searcher must not be wrapped
// into one.
func TestNotifyingStore_PreservesSearchCapability(t *testing.T) {
	t.Run("searcher base stays a searcher and still notifies", func(t *testing.T) {
		store := &searchableStore{
			mockPromptStore: newMockPromptStore(),
			result:          []prompt.ScoredPrompt{{Prompt: prompt.Prompt{Name: "hit"}, Score: 1}},
		}
		h := New(Config{Store: store})
		c := &countingNotifier{}
		h.SetListChangedNotifier(c)

		searcher, ok := h.Store().(prompt.Searcher)
		if !ok {
			t.Fatal("wrapped searcher store must still satisfy prompt.Searcher")
		}
		got, err := searcher.Search(context.Background(), prompt.SearchQuery{QueryText: "x"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(got) != 1 || got[0].Prompt.Name != "hit" {
			t.Errorf("Search delegation broken: got %+v", got)
		}

		// The search-capable wrapper still fires on writes.
		if err := h.Store().Create(context.Background(), &prompt.Prompt{Name: "p"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if c.n.Load() != 1 {
			t.Errorf("write through search-capable wrapper fired %d times, want 1", c.n.Load())
		}
	})

	t.Run("non-searcher base is not promoted to a searcher", func(t *testing.T) {
		h := New(Config{Store: newMockPromptStore()})
		if _, ok := h.Store().(prompt.Searcher); ok {
			t.Error("wrapping a non-searcher store must not fabricate a prompt.Searcher")
		}
	})
}

// collectionCapableStore is a base store that also carries the collection
// capability (the production postgres store's shape for #1010).
type collectionCapableStore struct {
	*mockPromptStore
	prompt.CollectionStore
}

// TestNotifyingStore_ExposesCollectionCapability proves the wrapper surfaces
// the base store's prompt.CollectionStore through the CollectionProvider
// accessor (collection writes need no notification hook, so the capability is
// exposed rather than decorated) and does not fabricate it for incapable
// bases.
func TestNotifyingStore_ExposesCollectionCapability(t *testing.T) {
	capable := &collectionCapableStore{mockPromptStore: newMockPromptStore()}
	h := New(Config{Store: capable})
	if got := prompt.AsCollectionStore(h.Store()); got == nil {
		t.Error("wrapped collection-capable store must resolve via AsCollectionStore")
	}

	h = New(Config{Store: newMockPromptStore()})
	if got := prompt.AsCollectionStore(h.Store()); got != nil {
		t.Errorf("wrapping an incapable store must not fabricate a CollectionStore, got %T", got)
	}
}

// TestSetListChangedNotifier_NilHandle proves the setter is safe on a nil Handle
// (mirrors SetEmbedder / SetShareStore). notifyListChanged is only ever reached
// through the notifying store built by New on a non-nil Handle, so it needs no
// nil-Handle guard of its own.
func TestSetListChangedNotifier_NilHandle(_ *testing.T) {
	var h *Handle
	h.SetListChangedNotifier(&countingNotifier{}) // must not panic
}

var _ ListChangedNotifier = (*countingNotifier)(nil)
