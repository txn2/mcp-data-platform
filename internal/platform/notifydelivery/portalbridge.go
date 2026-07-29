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
// thread's target (title, deep link, owner email) and the people attached to
// it.
type PortalStores struct {
	Assets         portal.AssetStore
	Collections    portal.CollectionStore
	Prompts        portal.PromptStore
	KnowledgePages knowledgepage.Store
	// Grantees lists the people holding an explicit grant on a thread target.
	// Optional: nil narrows thread notifications to the target owner and the
	// thread author.
	Grantees ThreadGrantees
}

// ThreadGrantees lists the addresses holding an explicit grant on a thread
// target: its owner and the recipients of active shares. Satisfied by
// pkg/portal/mention.Audience.
type ThreadGrantees interface {
	Grantees(ctx context.Context, targetType, targetID string) ([]string, error)
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
	// Asset/collection shares link to the token viewer. The link is not a
	// bearer credential: unless the share is public, the viewer resolves it
	// only for a signed-in recipient (#999).
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
	_, err := n.enq.Notify(ctx, share.SharedWithEmail, notification.CategoryShare, notification.Payload{
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

// NotifyThreadEvent queues the emails one thread event produces: a mention
// email for each person the body named, and a "new comment/feedback" email for
// everyone else attached to the target -- its owner, the thread author, and the
// people it is shared with.
//
// Mentions are queued first, per recipient: the author chose those addresses,
// so each one costs a token of their rate limit. The general fan-out then goes
// out as one batch, because the size of a target's share list is a property of
// the item rather than something the author picked.
//
// Only a mention that was actually queued removes someone from the general
// fan-out. A recipient whose mention was dropped -- they muted the mention
// category, or the enqueue failed -- has been told nothing, so they still get
// the comment notification they would have had without being named.
func (n *PortalNotifier) NotifyThreadEvent(ctx context.Context, thread *portal.Thread, actorEmail, body string, mentioned []string) {
	target := n.threadTarget(ctx, thread)
	payload := notification.Payload{
		ItemID:    thread.ID,
		ItemTitle: target.title,
		Actor:     actorEmail,
		Message:   notification.Snippet(body),
		Link:      target.link,
	}

	mentionPayload := payload
	mentionPayload.Kind = notification.KindMention
	notifiedByName := n.queueMentions(ctx,
		notification.RecipientsExcluding(actorEmail, mentioned...), mentionPayload)

	// An event whose author has no resolvable address cannot be shown not to
	// be a self-notification, and the target owner -- the likeliest recipient
	// of that mistake -- is always in the fan-out set. Drop the fan-out and
	// say so. Mentions above still went out: the body named those addresses
	// explicitly, so they are not guesses about who wrote this.
	if notification.NormalizeAddress(actorEmail) == "" {
		slog.Warn("notification: thread event has no actor address; skipping comment fan-out", // #nosec G706 -- structured slog call; thread ID is server-generated and sanitized
			"thread", logsan.SanitizeForLog(thread.ID))
		return
	}

	// Conversational kinds read as comments; evaluative kinds (rating,
	// correction, approval, rejection, suggestion) read as feedback.
	payload.Kind = notification.KindFeedback
	if thread.Kind == portal.ThreadKindComment || thread.Kind == portal.ThreadKindQuestion {
		payload.Kind = notification.KindComment
	}
	grantees := n.grantees(ctx, thread)
	candidates := make([]string, 0, len(grantees)+2)
	candidates = append(candidates, target.owner, thread.AuthorEmail)
	candidates = append(candidates, grantees...)
	general := excluding(notification.RecipientsExcluding(actorEmail, candidates...), notifiedByName)
	n.enq.NotifyFanout(ctx, general, notification.CategoryComment, payload)
}

// excluding returns the addresses in list that are not in drop, compared in
// normalized form so the mention list and the fan-out list agree on a person
// whose address reached them in different shapes.
func excluding(list, drop []string) []string {
	if len(drop) == 0 {
		return list
	}
	dropped := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropped[notification.NormalizeAddress(d)] = struct{}{}
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if _, skip := dropped[notification.NormalizeAddress(item)]; !skip {
			out = append(out, item)
		}
	}
	return out
}

// queueMentions enqueues the mention notification for each named recipient and
// returns the ones a row was written for -- the only people the general
// fan-out may then skip.
func (n *PortalNotifier) queueMentions(ctx context.Context, named []string, payload notification.Payload) []string {
	var notified []string
	for _, recipient := range named {
		queued, err := n.enq.Notify(ctx, recipient, notification.CategoryMention, payload)
		if err != nil {
			slog.Warn("notification: mention enqueue failed", // #nosec G706 -- structured slog call; error sanitized
				logKeyError, logsan.SanitizeForLog(err.Error()))
			continue
		}
		if queued {
			notified = append(notified, recipient)
		}
	}
	return notified
}

// grantees returns the people the thread's target is shared with, or none when
// no grantee source is wired or the lookup fails -- a comment must still
// notify the owner and the author.
func (n *PortalNotifier) grantees(ctx context.Context, thread *portal.Thread) []string {
	if n.stores.Grantees == nil {
		return nil
	}
	emails, err := n.stores.Grantees.Grantees(ctx, thread.TargetType, thread.TargetID())
	if err != nil {
		slog.Warn("notification: thread grantee lookup failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return emails
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
