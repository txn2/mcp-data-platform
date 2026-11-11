package notifydelivery

import (
	"cmp"
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
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
	// External reports whether an address has an external account (#2092).
	// An internal thread notifies none of them (#2227). nil treats no address
	// as one.
	External func(ctx context.Context, email string) bool
	// Participants lists the people a thread involves: everyone who wrote a
	// message in it and everyone a message mentions (#2512). nil narrows the
	// fan-out to the owner, the thread author and the grantees.
	Participants ThreadParticipants
	// Reach reports whether a person can open a thread's target now, by the
	// rule the target's read applies (#2512). Every comment recipient passes
	// it, so a person who lost access is told nothing. nil checks nobody.
	Reach TargetReach
	// General names who hears about a thread on the General (standalone)
	// channel, which has no owner, and about a thread whose target's owner
	// cannot be found (#2603). nil tells nobody beyond the thread's own people.
	General GeneralRecipients
	// Channels posts a new General thread to the channels the list names;
	// nil posts to none.
	Channels ChannelSender
	// Events reads a thread's messages, to name a reply on a thread that has
	// no item and no stored title by its first line (#2603). nil names it
	// generically.
	Events ThreadEvents
}

// ThreadEvents lists a thread's messages, oldest first. Satisfied by the
// PostgreSQL thread store.
type ThreadEvents interface {
	ListEvents(ctx context.Context, threadID string) ([]threads.ThreadEvent, error)
}

// GeneralRecipients is who the General feedback list reaches now. Satisfied
// by internal/portal/generalfeedback.Service.
type GeneralRecipients interface {
	Recipients(ctx context.Context) (people, channels []string, acknowledge bool)
}

// ChannelSender posts a payload to named channels. Satisfied by
// notifyalert.Sender.
type ChannelSender interface {
	Send(ctx context.Context, names []string, key string, p notification.Payload) int
}

// ThreadParticipants lists the people a thread involves. Satisfied by the
// PostgreSQL thread store (threads.EventEditor).
type ThreadParticipants interface {
	Participants(ctx context.Context, threadID string) ([]string, error)
}

// TargetReach reports whether the person at an address can open a thread
// target. Satisfied by internal/portal/threadreach.Reach.
type TargetReach interface {
	CanOpen(ctx context.Context, email, targetType, targetID string) bool
}

// EventRetractor takes a deleted thread message's text out of the
// notification queue (#2514). Satisfied by the PostgreSQL queue store.
type EventRetractor interface {
	RetractEvent(ctx context.Context, eventID string) error
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
	retract EventRetractor
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
	n := NewPortalNotifier(h.enqueuer, stores, baseURL)
	n.retract = h.retract
	return n
}

// RetractThreadMessage removes a deleted message's text from every
// notification it produced and drops the ones not yet sent (#2514).
func (n *PortalNotifier) RetractThreadMessage(ctx context.Context, eventID string) {
	if n.retract == nil {
		return
	}
	if err := n.retract.RetractEvent(ctx, eventID); err != nil {
		slog.WarnContext(ctx, "notification: retracting a deleted message failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// NotifyShare queues a "shared with you" email for a direct share.
// Token-only shares (no recipient email) queue nothing: a share targeted by
// user ID alone carries no email address anywhere in the system.
func (n *PortalNotifier) NotifyShare(ctx context.Context, share *portal.Share, ev portal.ShareEvent) {
	// Prompts have no public token viewer; link to the in-app prompt page.
	// Asset/collection shares link to the token viewer. The link is not a
	// bearer credential: unless the share is public, the viewer resolves it
	// only for a signed-in recipient (#999).
	link := ""
	switch {
	case ev.Kind == notification.KindPrompt:
		link = notification.PortalLink(n.baseURL, "/prompts/"+ev.ItemID)
	case ev.Kind == notification.KindResource:
		// A resource has no token viewer either; its page reads the share.
		link = notification.PortalLink(n.baseURL, "/resources/"+ev.ItemID)
	case share.Token != "" && n.baseURL != "":
		link = notification.PortalLink(n.baseURL, "/view/"+share.Token)
	}
	_, err := n.enq.Notify(ctx, share.SharedWithEmail, notification.CategoryShare, notification.Payload{
		Kind:      ev.Kind,
		ItemID:    ev.ItemID,
		ItemTitle: ev.ItemTitle,
		Actor:     share.CreatedBy,
		Message:   shareMessage(share, ev),
		Link:      link,
	})
	if err != nil {
		slog.WarnContext(ctx, "notification: share enqueue failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// AccessNotice is one email about a request for access (#2090): To is who is
// told, Actor who acted, Message the requester's note or the answer.
type AccessNotice struct {
	To, Actor, ItemTitle, ItemID, Message, Link string
}

// NotifyAccessRequest tells an item's owner that someone asked for access to
// it, with the link to the requests waiting on them.
func (n *PortalNotifier) NotifyAccessRequest(ctx context.Context, a AccessNotice) {
	a.Link = notification.PortalLink(n.baseURL, "/feedback#access-requests")
	n.enqueue(ctx, notification.KindAccessRequest, a)
}

// NotifyAccessDecision tells a requester how their request was answered;
// approved, the link opens the item.
func (n *PortalNotifier) NotifyAccessDecision(ctx context.Context, a AccessNotice) {
	n.enqueue(ctx, notification.KindAccessDecision, a)
}

// NotifyApprovalRequest tells each approver that a request waits on them
// (#2437, #2494), once, with the link to the Approvals page.
func (n *PortalNotifier) NotifyApprovalRequest(ctx context.Context, approvers []string, a AccessNotice) {
	a.Link = notification.PortalLink(n.baseURL, "/approvals")
	for _, to := range approvers {
		a.To = to
		n.enqueueKind(ctx, notification.KindApprovalRequest, a, "approval request")
	}
}

// NotifyApprovalDecision tells a requester how their request was decided.
func (n *PortalNotifier) NotifyApprovalDecision(ctx context.Context, a AccessNotice) {
	a.Link = notification.PortalLink(n.baseURL, "/approvals")
	n.enqueueKind(ctx, notification.KindApprovalDecision, a, "approval decision")
}

func (n *PortalNotifier) enqueue(ctx context.Context, kind string, a AccessNotice) {
	n.enqueueKind(ctx, kind, a, "access request")
}

func (n *PortalNotifier) enqueueKind(ctx context.Context, kind string, a AccessNotice, what string) {
	p := notification.Payload{
		Kind: kind, ItemID: a.ItemID, ItemTitle: a.ItemTitle, Actor: a.Actor, Message: a.Message, Link: a.Link,
	}
	if _, err := n.enq.Notify(ctx, a.To, notification.CategoryShare, p); err != nil {
		slog.WarnContext(ctx, "notification: enqueue failed", "what", what, // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// shareMessage picks the quoted note the share email renders.
//
// The sharer's own note wins. A custom notice falls back into that slot
// because it is text the sharer typed about this share and predates the note
// field; the default confidentiality banner never does, being viewer chrome
// rather than anything the sharer wrote.
func shareMessage(share *portal.Share, ev portal.ShareEvent) string {
	if ev.Message != "" {
		return ev.Message
	}
	if share.NoticeText == portal.DefaultNoticeText {
		return ""
	}
	return share.NoticeText
}

// NotifyThreadEvent queues the notifications one thread event produces. It is
// NotifyThreadMessage for a caller that does not name the message.
func (n *PortalNotifier) NotifyThreadEvent(ctx context.Context, thread *portal.Thread, actorEmail, body string, mentioned []string) {
	n.NotifyThreadMessage(ctx, threads.MessageNotice{Thread: thread, ActorEmail: actorEmail, Body: body, Mentioned: mentioned})
}

// NotifyThreadMessage queues the notifications one message produces: a
// mention for each person the body named, and a "new comment/feedback"
// notification for everyone else attached to the thread -- the target's
// owner, the people it is shared with, and its participants: the thread's
// author, everyone who wrote in it and everyone mentioned in it (#2512).
// Every one of them must be able to open the target now; a person who lost
// access is told nothing. Each row names the message, so deleting it can take
// its text back out of the queue (#2514).
//
// Mentions are queued first, per recipient: the author chose those addresses,
// so each one costs a token of their rate limit. The general fan-out then goes
// out as one batch, because who a thread involves is a property of the
// thread rather than something the author picked.
//
// Only a mention that was actually queued removes someone from the general
// fan-out. A recipient whose mention was dropped -- they muted the mention
// category, or the enqueue failed -- has been told nothing, so they still get
// the comment notification they would have had without being named.
func (n *PortalNotifier) NotifyThreadMessage(ctx context.Context, msg threads.MessageNotice) {
	thread := msg.Thread
	target := n.threadTarget(ctx, thread)
	if target.title == "" {
		target.title = n.headingOf(ctx, msg)
	}
	payload := notification.Payload{
		ItemID:    thread.ID,
		ItemTitle: target.title,
		Actor:     msg.ActorEmail,
		Message:   notification.Snippet(msg.Body),
		Link:      target.link,
		EventID:   msg.EventID,
	}

	mentionPayload := payload
	mentionPayload.Kind = notification.KindMention
	notifiedByName := n.queueMentions(ctx,
		notification.RecipientsExcluding(msg.ActorEmail, msg.Mentioned...), mentionPayload)
	if msg.MentionsOnly {
		return
	}

	// An event whose author has no resolvable address cannot be shown not to
	// be a self-notification, and the target owner -- the likeliest recipient
	// of that mistake -- is always in the fan-out set. Drop the fan-out and
	// say so. Mentions above still went out: the body named those addresses
	// explicitly, so they are not guesses about who wrote this.
	if notification.NormalizeAddress(msg.ActorEmail) == "" {
		slog.WarnContext(ctx, "notification: thread event has no actor address; skipping comment fan-out", // #nosec G706 -- structured slog call; thread ID is server-generated and sanitized
			"thread", logsan.SanitizeForLog(thread.ID))
		return
	}

	// Conversational kinds read as comments; evaluative kinds (rating,
	// correction, approval, rejection, suggestion) read as feedback.
	payload.Kind = notification.KindFeedback
	if thread.Kind == portal.ThreadKindComment || thread.Kind == portal.ThreadKindQuestion {
		payload.Kind = notification.KindComment
	}
	candidates := notification.RecipientsExcluding(msg.ActorEmail, n.involved(ctx, thread, target.owner)...)
	general := excluding(n.reachable(ctx, thread, candidates), notifiedByName)
	route := n.routeFor(ctx, thread, target)
	routed := excluding(notification.RecipientsExcluding(msg.ActorEmail, route.people...), notifiedByName)
	general = n.withRouted(ctx, thread, general, routed, route.standalone)
	queued := n.enq.NotifyFanout(ctx, n.audienceOf(ctx, thread, general), notification.CategoryComment, payload)
	if route.standalone && msg.Opened {
		n.openedGeneral(ctx, msg, payload, route, len(queued)+len(notifiedByName))
	}
}

// generalRoute is who the General feedback list adds to a thread's fan-out.
type generalRoute struct {
	people, channels []string
	acknowledge      bool
	standalone       bool
}

// routeFor reads the General feedback list for a thread that has no owner to
// tell: one on the General channel, or one whose target's owner cannot be
// found (a deleted item, a page with no recorded author). Any other thread
// takes nothing from it.
func (n *PortalNotifier) routeFor(ctx context.Context, thread *portal.Thread, target threadTargetInfo) generalRoute {
	route := generalRoute{standalone: thread.TargetType == targetStandalone}
	if n.stores.General == nil || (!route.standalone && target.owner != "") {
		return route
	}
	route.people, route.channels, route.acknowledge = n.stores.General.Recipients(ctx)
	return route
}

// withRouted adds the list's people (the actor and anyone told by name
// already taken out) to the fan-out. On the General channel
// they are told whether or not the directory holds them: a General thread is
// readable by everyone signed in, and an address an administrator typed (a
// team list) is no portal account. On an item that lost its owner, each must
// still be able to open the item.
func (n *PortalNotifier) withRouted(ctx context.Context, thread *portal.Thread, general, routed []string, standalone bool) []string {
	if len(routed) == 0 {
		return general
	}
	if !standalone {
		routed = n.reachable(ctx, thread, routed)
	}
	return notification.RecipientsExcluding("", append(general, routed...)...)
}

// openedGeneral finishes a new General thread (#2603): it is sent to the
// list's channels, its author is told it was received when the list says so
// and someone was, and a post that reached nobody is logged rather than lost
// in silence.
func (n *PortalNotifier) openedGeneral(ctx context.Context, msg threads.MessageNotice, payload notification.Payload, route generalRoute, told int) {
	if n.stores.Channels != nil && len(route.channels) > 0 {
		told += n.stores.Channels.Send(ctx, route.channels, "general_feedback:"+msg.Thread.ID, payload)
	}
	if told == 0 {
		slog.WarnContext(ctx, "notification: a new General feedback thread reached nobody; set Admin > Settings > General feedback", // #nosec G706 -- structured slog call; thread ID sanitized
			"thread", logsan.SanitizeForLog(msg.Thread.ID))
		return
	}
	if !route.acknowledge {
		return
	}
	if _, err := n.enq.Notify(ctx, msg.ActorEmail, notification.CategoryComment, notification.Payload{
		Kind: notification.KindFeedbackReceived, ItemID: msg.Thread.ID, ItemTitle: payload.ItemTitle, Link: payload.Link,
	}); err != nil {
		slog.WarnContext(ctx, "notification: acknowledging a General feedback post failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
	}
}

// involved lists everyone attached to a thread: the target's owner, the
// thread's author, its participants and the target's grantees.
func (n *PortalNotifier) involved(ctx context.Context, thread *portal.Thread, owner string) []string {
	grantees := n.grantees(ctx, thread)
	participants := n.participants(ctx, thread)
	out := []string{owner, thread.AuthorEmail}
	out = append(out, participants...)
	return append(out, grantees...)
}

// participants lists the people who wrote in the thread or are mentioned in
// it, or none when no source is wired or the lookup fails.
func (n *PortalNotifier) participants(ctx context.Context, thread *portal.Thread) []string {
	if n.stores.Participants == nil {
		return nil
	}
	people, err := n.stores.Participants.Participants(ctx, thread.ID)
	if err != nil {
		slog.WarnContext(ctx, "notification: thread participant lookup failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return people
}

// reachable keeps the recipients who can open the thread's target now. The
// author of a General thread opened it, and a General thread is readable by
// everyone signed in, so the author is kept whether or not the directory holds
// them (an API key's caller is not in it) (#2603).
func (n *PortalNotifier) reachable(ctx context.Context, thread *portal.Thread, recipients []string) []string {
	if n.stores.Reach == nil {
		return recipients
	}
	author := ""
	if thread.TargetType == targetStandalone {
		author = notification.NormalizeAddress(thread.AuthorEmail)
	}
	out := make([]string, 0, len(recipients))
	for _, r := range recipients {
		if (author != "" && notification.NormalizeAddress(r) == author) ||
			n.stores.Reach.CanOpen(ctx, r, thread.TargetType, thread.TargetID()) {
			out = append(out, r)
		}
	}
	return out
}

// audienceOf drops the external accounts from an internal thread's fan-out
// (#2227): a person outside the organization learns only of shared threads.
func (n *PortalNotifier) audienceOf(ctx context.Context, thread *portal.Thread, recipients []string) []string {
	if thread.Visibility == threads.VisibilityShared || n.stores.External == nil {
		return recipients
	}
	out := make([]string, 0, len(recipients))
	for _, r := range recipients {
		if !n.stores.External(ctx, r) {
			out = append(out, r)
		}
	}
	return out
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
			slog.WarnContext(ctx, "notification: mention enqueue failed", // #nosec G706 -- structured slog call; error sanitized
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
		slog.WarnContext(ctx, "notification: thread grantee lookup failed", // #nosec G706 -- structured slog call; error sanitized
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
// owner email. A standalone thread links to itself on the Feedback page; a
// missing row yields the thread title with no link or owner.
func (n *PortalNotifier) threadTarget(ctx context.Context, thread *portal.Thread) threadTargetInfo {
	if info := n.resolveThreadTarget(ctx, thread); info != nil {
		return *info
	}
	info := threadTargetInfo{title: thread.Title}
	if thread.TargetType == targetStandalone {
		// A General thread opens on the Feedback page, which reads the
		// thread from the address (#2603).
		info.link = notification.PortalLink(n.baseURL, "/feedback#thread-"+thread.ID)
	}
	return info
}

// targetStandalone is a General (standalone) thread's target type.
const targetStandalone = "standalone"

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

// headingOf is how a thread with no item, and no stored title, is named in a
// notification: the first line of its first message, as every list shows it
// (#2513). On the message that opens the thread that line is the message's
// own; on a reply it is the first message with text the store holds (#2603).
func (n *PortalNotifier) headingOf(ctx context.Context, msg threads.MessageNotice) string {
	if msg.Opened {
		return cmp.Or(threads.FirstLineSnippet(msg.Body), generalHeading)
	}
	if n.stores.Events == nil {
		return generalHeading
	}
	events, err := n.stores.Events.ListEvents(ctx, msg.Thread.ID)
	if err != nil {
		slog.WarnContext(ctx, "notification: reading a thread's first message failed", // #nosec G706 -- structured slog call; error sanitized
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return generalHeading
	}
	for _, e := range events {
		if line := threads.FirstLineSnippet(e.Body); line != "" {
			return line
		}
	}
	return generalHeading
}

// generalHeading names a General thread whose first line is not at hand.
const generalHeading = "a General feedback thread"
