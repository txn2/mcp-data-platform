package promptlayer

import (
	"context"
	"sync/atomic"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// ListChangedNotifier schedules a debounced prompts/list_changed notification.
// *listchanged.Notifier satisfies it; the interface keeps this package free of
// the notifier's transport dependencies. A nil notifier (never bound) makes
// every write a silent no-op.
type ListChangedNotifier interface {
	Notify()
}

// SetListChangedNotifier binds the notifier fired after every prompt store
// write (create, update, delete). Called once the session broadcaster exists,
// after construction — like SetEmbedder / SetShareStore. Safe to call on a nil
// Handle. Because the notifying store wraps the backing store at construction
// and reads the notifier atomically per write, a prompt created before this
// binding simply does not notify (there are no connected clients yet), and
// every write after it does.
func (h *Handle) SetListChangedNotifier(n ListChangedNotifier) {
	if h == nil {
		return
	}
	h.listChanged.Store(&n)
}

// notifyListChanged fires the bound notifier, if any. Called by notifyingStore
// after a successful write. Nil-safe before binding.
func (h *Handle) notifyListChanged() {
	if p := h.listChanged.Load(); p != nil && *p != nil {
		(*p).Notify()
	}
}

// wrapStore returns base wrapped so every successful create/update/delete fires
// notify, preserving base's capability interfaces. A plain notifyingStore embeds
// prompt.Store and adds the write hooks; when base also implements the search
// extension (the production postgres store does, some test stores do not), the
// returned value additionally implements prompt.Searcher / knowledge.PromptSearcher
// by delegating Search to base, so the up-casts at
// promptlayer/tool.go, portal/prompt_handler.go, and searchfed still succeed.
// (knowledge.PromptSearcher is Search + GetByID; GetByID comes from the embedded
// prompt.Store, so implementing Search is sufficient for both.)
//
// Invariant: the ONLY extension methods any caller up-casts the prompt store to
// beyond prompt.Store are Search (prompt.Searcher) and the versioning methods
// (prompt.VersionStore, asserted by prompt.ApplyEdit and the composition
// root). If a future extension interface is introduced, it must be forwarded
// here too, or the wrapper will silently drop it. Capabilities that need no
// write hook (prompt.CollectionStore) are instead exposed through the
// prompt.CollectionProvider accessor on notifyingStore, which every wrapper
// shape inherits, so they add no combinations here.
func wrapStore(base prompt.Store, notify func(), guard attachmentGuard) prompt.Store {
	ns := &notifyingStore{Store: base, notify: notify, guard: guard}
	searcher, hasSearch := base.(prompt.Searcher)
	versions, hasVersions := base.(prompt.VersionStore)
	nvs := notifyingVersionStore{VersionStore: versions, notify: notify, guard: guard}
	switch {
	case hasSearch && hasVersions:
		return &notifyingSearchVersionStore{
			notifyingSearchStore:  notifyingSearchStore{notifyingStore: ns, searcher: searcher},
			notifyingVersionStore: nvs,
		}
	case hasSearch:
		return &notifyingSearchStore{notifyingStore: ns, searcher: searcher}
	case hasVersions:
		return &notifyingVersionOnlyStore{notifyingStore: ns, notifyingVersionStore: nvs}
	default:
		return ns
	}
}

// notifyingStore decorates a prompt.Store so every successful create, update, or
// delete fires the prompts/list_changed notifier. Wrapping the shared store —
// the single instance the manage_prompt tool, the admin/portal REST handlers,
// and the knowledge add_prompt path all write through — makes the notification
// a property of the write itself, so no write path can add or remove a prompt
// without a connected client learning the prompt set changed. Read methods pass
// through untouched via the embedded interface.
type notifyingStore struct {
	prompt.Store
	notify func()
	guard  attachmentGuard
}

// attachmentGuard reports whether a prompt about to be written would leave one
// of its attached resources unreachable for the audience the write gives it
// (#1013). It is applied here, on the shared store, for the same reason
// list_changed is: every path that writes a prompt crosses it, so making the
// check a property of the write means no writer — the manage_prompt tool, the
// portal editor, an admin promotion approval — can widen a prompt's scope past
// its materials, and none of them needs its own copy of the rule.
//
// It governs scope, not readership. Sharing a personal prompt person-to-person
// writes to the share store, not the prompt, so it does not pass through here;
// a recipient of a prompt carrying the author's private template receives it
// with that material reported as undelivered, which the serve-time check
// guarantees. A nil guard skips the check.
type attachmentGuard func(ctx context.Context, p *prompt.Prompt) error

// checkAttachments applies the guard, if bound.
func (s *notifyingStore) checkAttachments(ctx context.Context, p *prompt.Prompt) error {
	if s.guard == nil {
		return nil
	}
	return s.guard(ctx, p)
}

// notifyingSearchStore is notifyingStore for a base that also implements the
// search extension. It re-exposes Search (captured at construction so the
// delegation cannot panic) while inheriting the write hooks and read pass-through
// from the embedded *notifyingStore.
type notifyingSearchStore struct {
	*notifyingStore
	searcher prompt.Searcher
}

// The store methods below are a transparent decorator: they return the backing
// store's error UNCHANGED (no fmt.Errorf wrapping) so callers' errors.Is / As
// checks against the underlying store's sentinels keep working. The //nolint on
// each is deliberate — wrapping here would be a behavior change, not a fix.

// Search delegates to the captured base searcher, preserving the prompt.Searcher
// / knowledge.PromptSearcher capability through the wrapper.
func (s *notifyingSearchStore) Search(ctx context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return s.searcher.Search(ctx, q) //nolint:wrapcheck // transparent decorator: pass the searcher's error through unchanged
}

// Collections exposes the wrapped store's collection capability (#1010) via
// prompt.CollectionProvider. Collection writes organize the portal library
// only — they never change the MCP prompt list, so they need no list_changed
// hook and pass through undecorated. Defined on the embedded base wrapper so
// every capability-combination shape built in wrapStore inherits it.
func (s *notifyingStore) Collections() prompt.CollectionStore {
	return prompt.AsCollectionStore(s.Store)
}

// Attachments exposes the wrapped store's prompt-attachment capability (#1013)
// via prompt.AttachmentProvider. Attachment writes change a prompt's reference
// material, not the prompt list, so like collection writes they need no
// list_changed hook and pass through undecorated.
func (s *notifyingStore) Attachments() prompt.AttachmentStore {
	return prompt.AsAttachmentStore(s.Store)
}

// Create persists a new prompt and notifies on success.
func (s *notifyingStore) Create(ctx context.Context, p *prompt.Prompt) error {
	if err := s.Store.Create(ctx, p); err != nil {
		return err //nolint:wrapcheck // transparent decorator: pass the store's error through unchanged
	}
	s.notify()
	return nil
}

// Update modifies a prompt and notifies on success. The attachment guard runs
// first: a scope change that strands the prompt's materials must be refused
// before it is persisted, not detected afterwards.
func (s *notifyingStore) Update(ctx context.Context, p *prompt.Prompt) error {
	if err := s.checkAttachments(ctx, p); err != nil {
		return err
	}
	if err := s.Store.Update(ctx, p); err != nil {
		return err //nolint:wrapcheck // transparent decorator: pass the store's error through unchanged
	}
	s.notify()
	return nil
}

// Delete removes a prompt by name and notifies on success.
func (s *notifyingStore) Delete(ctx context.Context, name string) error {
	if err := s.Store.Delete(ctx, name); err != nil {
		return err //nolint:wrapcheck // transparent decorator: pass the store's error through unchanged
	}
	s.notify()
	return nil
}

// DeleteByID removes a prompt by ID and notifies on success.
func (s *notifyingStore) DeleteByID(ctx context.Context, id string) error {
	if err := s.Store.DeleteByID(ctx, id); err != nil {
		return err //nolint:wrapcheck // transparent decorator: pass the store's error through unchanged
	}
	s.notify()
	return nil
}

// notifyingVersionStore decorates the store's versioning capability so the
// version writes that change what is served or listed (an applied update, an
// approved draft) fire the same prompts/list_changed notifier as the plain
// store writes. CreateDraftVersion and RejectVersion never change the served
// set, so they pass through without notifying. Embedded by the capability
// combination wrappers built in wrapStore.
type notifyingVersionStore struct {
	prompt.VersionStore
	notify func()
	guard  attachmentGuard
}

// notifyingVersionOnlyStore combines the write hooks with the versioning
// capability for a base store without search.
type notifyingVersionOnlyStore struct {
	*notifyingStore
	notifyingVersionStore
}

// notifyingSearchVersionStore combines the write hooks with both capability
// extensions — the production postgres store's shape.
type notifyingSearchVersionStore struct {
	notifyingSearchStore
	notifyingVersionStore
}

// UpdateWithVersion applies a versioned update and notifies on success. Like
// Update, it clears the attachment guard first.
func (s *notifyingVersionStore) UpdateWithVersion(ctx context.Context, p *prompt.Prompt, author string) error {
	if s.guard != nil {
		if err := s.guard(ctx, p); err != nil {
			return err
		}
	}
	if err := s.VersionStore.UpdateWithVersion(ctx, p, author); err != nil {
		return err //nolint:wrapcheck // transparent decorator: pass the store's error through unchanged
	}
	s.notify()
	return nil
}

// ApproveVersion applies a draft snapshot to the live prompt and notifies on
// success (the served content changed).
func (s *notifyingVersionStore) ApproveVersion(ctx context.Context, promptID string, version int, approver string) (*prompt.Prompt, error) {
	p, err := s.VersionStore.ApproveVersion(ctx, promptID, version, approver)
	if err != nil {
		return nil, err //nolint:wrapcheck // transparent decorator: pass the store's error through unchanged
	}
	s.notify()
	return p, nil
}

// atomicNotifier is the atomic slot type holding the bound notifier. Declared
// as a field type alias so the Handle can zero-initialize it.
type atomicNotifier = atomic.Pointer[ListChangedNotifier]

// Compile-time guarantee that the search-capable wrapper preserves the search
// extension (and thus knowledge.PromptSearcher, which is Search + the embedded
// GetByID) so the up-casts across the codebase continue to succeed.
var (
	_ prompt.Store              = (*notifyingStore)(nil)
	_ prompt.CollectionProvider = (*notifyingStore)(nil)
	_ prompt.AttachmentProvider = (*notifyingStore)(nil)
	_ prompt.CollectionProvider = (*notifyingSearchVersionStore)(nil)
	_ prompt.Store              = (*notifyingSearchStore)(nil)
	_ prompt.Searcher           = (*notifyingSearchStore)(nil)
	_ prompt.Store              = (*notifyingVersionOnlyStore)(nil)
	_ prompt.VersionStore       = (*notifyingVersionOnlyStore)(nil)
	_ prompt.Store              = (*notifyingSearchVersionStore)(nil)
	_ prompt.Searcher           = (*notifyingSearchVersionStore)(nil)
	_ prompt.VersionStore       = (*notifyingSearchVersionStore)(nil)
)
