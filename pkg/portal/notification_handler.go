package portal

import "context"

// Notifier receives the portal's notification trigger events (issue #631):
// direct shares and feedback-thread activity. Implementations queue email
// notifications and must never fail the originating request -- both methods
// are fire-and-forget, logging their own errors. The share kind is one of
// "asset", "collection", or "prompt". A nil Notifier disables notifications.
//
// The implementation lives with the notification delivery substrate
// (internal/platform/notifydelivery); portal only fires the events.
type Notifier interface {
	// NotifyShare fires after a successful direct-share insert.
	NotifyShare(ctx context.Context, share *Share, kind, itemID, itemTitle string)
	// NotifyThreadEvent fires after a successful thread create or event
	// append. thread carries the target reference; body is the comment text;
	// mentioned carries the @-mentions the body delivers, already filtered to
	// people who can open the target (#627). Mentioned people are notified as
	// mentions -- their own category, which a recipient can keep on while
	// muting general thread activity -- and are left out of the general
	// notification, so one comment never sends the same person two emails.
	NotifyThreadEvent(ctx context.Context, thread *Thread, actorEmail, body string, mentioned []string)
}

// notifyShare fires the share trigger when a notifier is wired.
func (h *Handler) notifyShare(ctx context.Context, share *Share, kind, itemID, itemTitle string) {
	if h.deps.Notifier != nil {
		h.deps.Notifier.NotifyShare(ctx, share, kind, itemID, itemTitle)
	}
}

// notifyThreadEvent fires the thread trigger when a notifier is wired.
func (h *Handler) notifyThreadEvent(ctx context.Context, thread *Thread, actorEmail, body string, mentioned []string) {
	if h.deps.Notifier != nil {
		h.deps.Notifier.NotifyThreadEvent(ctx, thread, actorEmail, body, mentioned)
	}
}
