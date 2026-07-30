package promptlayer

import (
	"sync/atomic"
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

// notifyListChanged fires the bound notifier, if any. Called by the
// notifystore wrapper after a successful write. Nil-safe before binding.
func (h *Handle) notifyListChanged() {
	if p := h.listChanged.Load(); p != nil && *p != nil {
		(*p).Notify()
	}
}

// atomicNotifier is the atomic slot type holding the bound notifier. Declared
// as a field type alias so the Handle can zero-initialize it.
type atomicNotifier = atomic.Pointer[ListChangedNotifier]
