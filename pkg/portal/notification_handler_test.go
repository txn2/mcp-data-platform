package portal

import (
	"context"
	"testing"
)

// recordingNotifier captures trigger events for assertion.
type recordingNotifier struct {
	shares  int
	threads int
}

func (n *recordingNotifier) NotifyShare(_ context.Context, _ *Share, _, _, _ string) {
	n.shares++
}

func (n *recordingNotifier) NotifyThreadEvent(_ context.Context, _ *Thread, _, _ string) {
	n.threads++
}

func TestNotifyWrappers(t *testing.T) {
	rec := &recordingNotifier{}
	h := &Handler{deps: Deps{Notifier: rec}}

	h.notifyShare(context.Background(), &Share{}, "asset", "a1", "Report")
	h.notifyThreadEvent(context.Background(), &Thread{}, "a@b.io", "hi")

	if rec.shares != 1 || rec.threads != 1 {
		t.Errorf("triggers not forwarded: shares=%d threads=%d", rec.shares, rec.threads)
	}
}

func TestNotifyWrappers_NilNotifier(_ *testing.T) {
	h := &Handler{deps: Deps{}}
	// Must be a silent no-op, never a panic.
	h.notifyShare(context.Background(), &Share{}, "asset", "a1", "Report")
	h.notifyThreadEvent(context.Background(), &Thread{}, "a@b.io", "hi")
}
