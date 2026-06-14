package portal

import (
	"context"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Feedback activity feed (#617). A single self-scoped, cross-artifact view of
// every feedback thread on items the caller can view (owned or shared assets,
// collections, and prompts), most recent activity first. It doubles as a
// lightweight notification surface: with no push notifications, this is how a
// user discovers that something they can access has new feedback. Global-prompt
// chatter is intentionally excluded so the feed stays personal to the caller.

// threadActivityItem is an activity-feed row: a thread enriched with a
// human-readable label for the target it lives on, so the client can render a
// link back to the item without a per-row lookup.
type threadActivityItem struct {
	ThreadWithMeta
	TargetLabel string `json:"target_label"`
}

// feedbackActivity handles GET /api/v1/portal/feedback/activity.
//
// @Summary      Feedback activity feed
// @Description  Lists feedback threads across every asset, collection, and prompt the caller can view (owned or shared), most recent activity first. Each row carries the target's display label so the client can link back to it. Scoped server-side to the caller's accessible items, so it never discloses feedback on items the caller cannot see.
// @Tags         Feedback
// @Produce      json
// @Param        limit   query  int  false  "Page size"
// @Param        offset  query  int  false  "Page offset"
// @Success      200  {object}  paginatedResponse
// @Failure      401  {object}  problemDetail
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /portal/feedback/activity [get]
func (h *Handler) feedbackActivity(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, errAuthRequired)
		return
	}

	targets, err := h.viewableActivityTargets(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve your artifacts")
		return
	}
	// No accessible artifacts: nothing to show. Do not fall through to an
	// unscoped ListThreads, which would disclose feedback on items the caller
	// cannot see.
	if targets.empty() {
		writeJSON(w, http.StatusOK, paginatedResponse{Data: []threadActivityItem{}, Limit: defaultThreadLimit})
		return
	}

	filter := ThreadFilter{
		TargetAssetIDs:      targets.assetIDs,
		TargetCollectionIDs: targets.collIDs,
		TargetPromptIDs:     targets.promptIDs,
		Limit:               intParam(r, paramLimit, defaultThreadLimit),
		Offset:              intParam(r, paramOffset, 0),
	}
	threads, total, err := h.deps.ThreadStore.ListThreads(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load feedback activity")
		return
	}
	writeJSON(w, http.StatusOK, paginatedResponse{
		Data:   h.labelActivityThreads(r.Context(), threads),
		Total:  total,
		Limit:  filter.EffectiveLimit(),
		Offset: filter.Offset,
	})
}

// activityTargets bundles the target ids whose feedback belongs in a caller's
// activity feed, one slice per target type.
type activityTargets struct {
	assetIDs  []string
	collIDs   []string
	promptIDs []string
}

// empty reports whether the caller can reach no feedback targets at all, so the
// handler can return an empty feed without an unscoped query.
func (t activityTargets) empty() bool {
	return len(t.assetIDs) == 0 && len(t.collIDs) == 0 && len(t.promptIDs) == 0
}

// viewableActivityTargets returns the assets, collections, and prompts whose
// feedback belongs in the caller's personal feed: items they own plus items
// shared with them at any permission (assets/collections via the shared
// gatherAssetIDs/gatherCollectionIDs with keepAnyShare), and their personal or
// shared prompts. This is deliberately a personal scope, NOT the full per-thread
// access set: an admin (who could open every thread) and global prompts (visible
// to everyone) are excluded so the feed does not become a platform-wide
// firehose. Gathered up front so the feed query is scoped to the caller's
// reachable set and never discloses feedback on unreachable items.
func (h *Handler) viewableActivityTargets(ctx context.Context, user *User) (activityTargets, error) {
	var t activityTargets
	var err error
	g := h.targetGatherer(user)
	if t.assetIDs, err = g.AssetIDs(ctx, KeepAnyShare); err != nil {
		return activityTargets{}, err
	}
	if t.collIDs, err = g.CollectionIDs(ctx, KeepAnyShare); err != nil {
		return activityTargets{}, err
	}
	if t.promptIDs, err = h.viewablePromptIDs(ctx, user); err != nil {
		return activityTargets{}, err
	}
	return t, nil
}

// viewablePromptIDs gathers the prompts the user can view by ownership or share:
// their personal prompts plus prompts shared with them. Global prompts are
// visible to everyone and excluded on purpose, so the feed is not flooded with
// feedback on shared-library prompts the user never authored.
func (h *Handler) viewablePromptIDs(ctx context.Context, user *User) ([]string, error) {
	if h.deps.PromptStore == nil {
		return nil, nil
	}
	owned, err := h.deps.PromptStore.List(ctx, prompt.ListFilter{Scope: prompt.ScopePersonal, OwnerEmail: user.Email})
	if err != nil {
		return nil, fmt.Errorf("listing owned prompts: %w", err)
	}
	var ids []string
	for _, p := range owned {
		ids = append(ids, p.ID)
	}
	if h.deps.ShareStore != nil {
		refs, err := h.deps.ShareStore.ListSharedPromptsWithUser(ctx, user.UserID, user.Email)
		if err != nil {
			return nil, fmt.Errorf("listing shared prompts: %w", err)
		}
		for _, ref := range refs {
			ids = append(ids, ref.PromptID)
		}
	}
	return ids, nil
}

// labelActivityThreads enriches each thread with its target's display label,
// resolving names in batches keyed by target type to avoid a per-row fan-out.
func (h *Handler) labelActivityThreads(ctx context.Context, threads []ThreadWithMeta) []threadActivityItem {
	assetIDs, collIDs, promptIDs := partitionTargetIDs(threads)
	assets := h.assetNames(ctx, assetIDs)
	colls := h.collectionNames(ctx, collIDs)
	prompts := h.promptNames(ctx, promptIDs)
	items := make([]threadActivityItem, 0, len(threads))
	for _, t := range threads {
		items = append(items, threadActivityItem{
			ThreadWithMeta: t,
			TargetLabel:    targetActivityLabel(t, assets, colls, prompts),
		})
	}
	return items
}

// partitionTargetIDs returns the unique asset, collection, and prompt ids
// referenced by the given threads, so each target type is resolved in one batch.
// Dedup is per target type (a separate seen set each) so an id that happens to
// collide across id spaces is not dropped from one of them.
func partitionTargetIDs(threads []ThreadWithMeta) (assetIDs, collIDs, promptIDs []string) {
	seenA, seenC, seenP := map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(dst *[]string, seen map[string]bool, id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		*dst = append(*dst, id)
	}
	for i := range threads {
		switch threads[i].TargetType {
		case targetTypeAsset:
			add(&assetIDs, seenA, threads[i].AssetID)
		case targetTypeCollection:
			add(&collIDs, seenC, threads[i].CollectionID)
		case targetTypePrompt:
			add(&promptIDs, seenP, threads[i].PromptID)
		}
	}
	return assetIDs, collIDs, promptIDs
}

// assetNames resolves asset ids to display names in one batch.
func (h *Handler) assetNames(ctx context.Context, ids []string) map[string]string {
	if h.deps.AssetStore == nil || len(ids) == 0 {
		return nil
	}
	assets, err := h.deps.AssetStore.GetByIDs(ctx, ids)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(assets))
	for id, a := range assets {
		if a != nil {
			out[id] = a.Name
		}
	}
	return out
}

// collectionNames resolves collection ids to display names.
func (h *Handler) collectionNames(ctx context.Context, ids []string) map[string]string {
	if h.deps.CollectionStore == nil || len(ids) == 0 {
		return nil
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if c, err := h.deps.CollectionStore.Get(ctx, id); err == nil && c != nil {
			out[id] = c.Name
		}
	}
	return out
}

// promptNames resolves prompt ids to display names.
func (h *Handler) promptNames(ctx context.Context, ids []string) map[string]string {
	if h.deps.PromptStore == nil || len(ids) == 0 {
		return nil
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if p, err := h.deps.PromptStore.GetByID(ctx, id); err == nil && p != nil {
			out[id] = promptDisplayName(p)
		}
	}
	return out
}

// promptDisplayName prefers a prompt's display name, falling back to its name.
func promptDisplayName(p *prompt.Prompt) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}

// targetActivityLabel picks the display label for a thread's target, falling
// back to the target type when the name could not be resolved (e.g. a
// since-deleted item the caller still has a thread on).
func targetActivityLabel(t ThreadWithMeta, assets, colls, prompts map[string]string) string {
	switch t.TargetType {
	case targetTypeAsset:
		return labelOrFallback(assets[t.AssetID], "Asset")
	case targetTypeCollection:
		return labelOrFallback(colls[t.CollectionID], "Collection")
	case targetTypePrompt:
		return labelOrFallback(prompts[t.PromptID], "Prompt")
	default:
		return "General feedback"
	}
}

// labelOrFallback returns name when set, otherwise the fallback label.
func labelOrFallback(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}
