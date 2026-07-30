package portal

import (
	"context"
	"testing"
)

// recordingNotifier captures the share trigger for assertion. The thread
// trigger is fired by the feedback surface and asserted there.
type recordingNotifier struct {
	shares int
}

func (n *recordingNotifier) NotifyShare(_ context.Context, _ *Share, _, _, _ string) {
	n.shares++
}

func (*recordingNotifier) NotifyThreadEvent(_ context.Context, _ *Thread, _, _ string, _ []string) {}

func TestNotifyShareForwardsToTheNotifier(t *testing.T) {
	rec := &recordingNotifier{}
	h := &Handler{deps: Deps{Notifier: rec}}

	h.notifyShare(context.Background(), &Share{}, "asset", "a1", "Report")

	if rec.shares != 1 {
		t.Errorf("share trigger not forwarded: shares=%d", rec.shares)
	}
}

func TestNotifyShareWithNoNotifier(_ *testing.T) {
	h := &Handler{deps: Deps{}}
	// Must be a silent no-op, never a panic.
	h.notifyShare(context.Background(), &Share{}, "asset", "a1", "Report")
}
