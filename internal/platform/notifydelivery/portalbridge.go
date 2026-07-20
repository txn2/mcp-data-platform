package notifydelivery

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// PortalStores bundles the read access the notifier needs to resolve a
// thread's target (title, deep link, owner email).
type PortalStores struct {
	Assets         portal.AssetStore
	Collections    portal.CollectionStore
	Prompts        portal.PromptStore
	KnowledgePages knowledgepage.Store
}

// PortalNotifier implements portal.Notifier: it turns portal share and
// thread events into queued email notifications. All failures are logged,
// never surfaced -- a share or comment must succeed regardless of
// notification state.
type PortalNotifier struct {
	enq     *notification.Enqueuer
	stores  PortalStores
	baseURL string
}

// NewPortalNotifier builds a notifier over an enqueuer directly; tests and
// the handle-based constructor below share it.
func NewPortalNotifier(enq *notification.Enqueuer, stores PortalStores, baseURL string) *PortalNotifier {
	return &PortalNotifier{enq: enq, stores: stores, baseURL: baseURL}
}

// PortalNotifier builds the portal-facing notifier over this handle's
// enqueuer. Returns nil when the handle is nil (feature unavailable); the
// caller must then leave the portal's Notifier dependency unset.
func (h *Handle) PortalNotifier(stores PortalStores, baseURL string) *PortalNotifier {
	if h == nil {
		return nil
	}
	return NewPortalNotifier(h.enqueuer, stores, baseURL)
}

// NotifyShare queues a "shared with you" email for a direct share.
// Token-only shares (no recipient email) queue nothing: a share targeted by
// user ID alone carries no email address anywhere in the system.
func (n *PortalNotifier) NotifyShare(ctx context.Context, share *portal.Share, kind, itemID, itemTitle string) {
	// Prompts have no public token viewer; link to the in-app prompt page.
	// Asset/collection shares link to the token viewer, which works for
	// the recipient without signing in.
	link := ""
	switch {
	case kind == notification.KindPrompt:
		link = notification.PortalLink(n.baseURL, "/prompts/"+itemID)
	case share.Token != "" && n.baseURL != "":
		link = notification.PortalLink(n.baseURL, "/view/"+share.Token)
	}
	// The notice renders as a quoted personal message from the sharer, so
	// only a custom notice belongs there -- the default confidentiality
	// banner is viewer chrome, not something the sharer wrote.
	message := share.NoticeText
	if message == portal.DefaultNoticeText {
		message = ""
	}
	err := n.enq.Notify(ctx, share.SharedWithEmail, notification.CategoryShare, notification.Payload{
		Kind:      kind,
		ItemID:    itemID,
		ItemTitle: itemTitle,
		Actor:     share.CreatedBy,
		Message:   message,
		Link:      link,
	})
	if err != nil {
		slog.Warn("notification: share enqueue failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// NotifyThreadEvent queues a "new comment/feedback" email for the owner of
// the thread's target and, on replies, the thread author, excluding the
// actor.
func (n *PortalNotifier) NotifyThreadEvent(ctx context.Context, thread *portal.Thread, actorEmail, body string) {
	// Conversational kinds read as comments; evaluative kinds (rating,
	// correction, approval, rejection, suggestion) read as feedback.
	kind := notification.KindFeedback
	if thread.Kind == portal.ThreadKindComment || thread.Kind == portal.ThreadKindQuestion {
		kind = notification.KindComment
	}
	target := n.threadTarget(ctx, thread)
	payload := notification.Payload{
		Kind:      kind,
		ItemID:    thread.ID,
		ItemTitle: target.title,
		Actor:     actorEmail,
		Message:   notification.Snippet(body),
		Link:      target.link,
	}
	for _, recipient := range notification.RecipientsExcluding(actorEmail, target.owner, thread.AuthorEmail) {
		if err := n.enq.Notify(ctx, recipient, notification.CategoryComment, payload); err != nil {
			slog.Warn("notification: thread enqueue failed", // #nosec G706 -- structured slog call; error sanitized
				logKeyError, logsan.SanitizeForLog(err.Error()))
		}
	}
}

// threadTargetInfo carries the resolved target of a thread notification.
type threadTargetInfo struct {
	title string
	link  string
	owner string
}

// threadTarget resolves the thread's target title, portal deep link, and
// owner email. Unresolvable targets (standalone threads, missing rows)
// yield the thread title with no link or owner; the notification still
// renders without them.
func (n *PortalNotifier) threadTarget(ctx context.Context, thread *portal.Thread) threadTargetInfo {
	if info := n.resolveThreadTarget(ctx, thread); info != nil {
		return *info
	}
	return threadTargetInfo{title: thread.Title}
}

// resolveThreadTarget dispatches on the thread's target reference,
// returning nil when the target cannot be resolved.
func (n *PortalNotifier) resolveThreadTarget(ctx context.Context, thread *portal.Thread) *threadTargetInfo {
	switch {
	case thread.AssetID != "":
		return n.assetTarget(ctx, thread.AssetID)
	case thread.CollectionID != "":
		return n.collectionTarget(ctx, thread.CollectionID)
	case thread.PromptID != "":
		return n.promptTarget(ctx, thread.PromptID)
	case thread.KnowledgePageID != "":
		return n.knowledgePageTarget(ctx, thread.KnowledgePageID)
	}
	return nil
}

// assetTarget resolves an asset-targeted thread.
func (n *PortalNotifier) assetTarget(ctx context.Context, id string) *threadTargetInfo {
	asset, err := n.stores.Assets.Get(ctx, id)
	if err != nil {
		return nil
	}
	return &threadTargetInfo{
		title: asset.Name,
		link:  notification.PortalLink(n.baseURL, "/assets/"+asset.ID),
		owner: asset.OwnerEmail,
	}
}

// collectionTarget resolves a collection-targeted thread.
func (n *PortalNotifier) collectionTarget(ctx context.Context, id string) *threadTargetInfo {
	coll, err := n.stores.Collections.Get(ctx, id)
	if err != nil {
		return nil
	}
	return &threadTargetInfo{
		title: coll.Name,
		link:  notification.PortalLink(n.baseURL, "/collections/"+coll.ID),
		owner: coll.OwnerEmail,
	}
}

// promptTarget resolves a prompt-targeted thread.
func (n *PortalNotifier) promptTarget(ctx context.Context, id string) *threadTargetInfo {
	pr, err := n.stores.Prompts.GetByID(ctx, id)
	if err != nil || pr == nil {
		return nil
	}
	return &threadTargetInfo{
		title: pr.DisplayName,
		link:  notification.PortalLink(n.baseURL, "/prompts/"+pr.ID),
		owner: pr.OwnerEmail,
	}
}

// knowledgePageTarget resolves a knowledge-page-targeted thread.
func (n *PortalNotifier) knowledgePageTarget(ctx context.Context, id string) *threadTargetInfo {
	if n.stores.KnowledgePages == nil {
		return nil
	}
	page, err := n.stores.KnowledgePages.Get(ctx, id)
	if err != nil || page == nil {
		return nil
	}
	return &threadTargetInfo{
		title: page.Title,
		link:  notification.PortalLink(n.baseURL, "/knowledge/pages/"+page.ID),
		owner: page.CreatedEmail,
	}
}
