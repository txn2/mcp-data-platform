package portal

import (
	"context"
	"testing"
)

// recordingNotifier captures the share trigger for assertion. The thread
// trigger is fired by the feedback surface and asserted there.
type recordingNotifier struct {
	shares int
	last   ShareEvent
}

func (n *recordingNotifier) NotifyShare(_ context.Context, _ *Share, ev ShareEvent) {
	n.shares++
	n.last = ev
}

func (*recordingNotifier) NotifyThreadEvent(_ context.Context, _ *Thread, _, _ string, _ []string) {}

func TestNotifyShareForwardsToTheNotifier(t *testing.T) {
	rec := &recordingNotifier{}
	h := &Handler{deps: Deps{Notifier: rec}}

	h.notifyShare(context.Background(), &Share{},
		ShareEvent{Kind: "asset", ItemID: "a1", ItemTitle: "Report", Message: "have a look"})

	if rec.shares != 1 {
		t.Errorf("share trigger not forwarded: shares=%d", rec.shares)
	}
	if rec.last.Message != "have a look" {
		t.Errorf("sharer note not forwarded: message=%q", rec.last.Message)
	}
}

func TestNotifyShareWithNoNotifier(_ *testing.T) {
	h := &Handler{deps: Deps{}}
	// Must be a silent no-op, never a panic.
	h.notifyShare(context.Background(), &Share{}, ShareEvent{Kind: "asset", ItemID: "a1"})
}
