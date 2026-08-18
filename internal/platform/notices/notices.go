package notices

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// logKeyError is the slog key every swallowed digest failure is logged under.
const logKeyError = "error"

const (
	// maxOwnedAssets bounds the owned-asset set a single digest gathers, so a
	// caller with an enormous library cannot issue an unbounded query. It is
	// the store's own page ceiling; passing more would be clamped there anyway.
	maxOwnedAssets = portaldomain.MaxLimit
	// maxFeedbackNotices and maxShareNotices bound what one briefing names. The
	// digest is read aloud at the start of a session, so it is a short list plus
	// a count, not a mailbox.
	maxFeedbackNotices = 10
	maxShareNotices    = 10
	// firstRunLookback is the window a caller who has never been briefed is
	// briefed on. Without a watermark the platform cannot tell what they have
	// already seen in the portal, and reporting the whole history would
	// announce a two-year-old share as new.
	firstRunLookback = 30 * 24 * time.Hour
)

// Handle assembles a caller's session-start digest from the portal stores and
// the notice watermark. A nil Handle builds nothing, which is what a deployment
// without a database or without the portal holds.
type Handle struct {
	assets  portaldomain.AssetStore
	shares  portaldomain.ShareStore
	threads threads.ThreadStore
	marks   WatermarkStore
	// now is the clock, replaced in tests so a digest's watermark arithmetic is
	// deterministic.
	now func() time.Time
}

// New builds the digest assembler over db and the portal stores. It returns nil
// when any input is missing, so a caller wires it unconditionally and every
// deployment that lacks a piece simply carries no notices.
func New(db *sql.DB, assets portaldomain.AssetStore, shares portaldomain.ShareStore, ts threads.ThreadStore) *Handle {
	if db == nil || assets == nil || shares == nil || ts == nil {
		return nil
	}
	return &Handle{
		assets:  assets,
		shares:  shares,
		threads: ts,
		marks:   NewPostgresWatermarkStore(db),
		now:     time.Now,
	}
}

// caller is the identity one digest is built for. Threads and share grants are
// both matched by id OR email, so both are carried; key is the watermark's
// primary key.
type caller struct {
	id    string
	email string
	key   string
}

// callerOf resolves the digest identity from the request's platform context. It
// reports false for a caller there is no one to brief: an absent context, the
// shared anonymous identity, or an identity with neither an id nor an email.
func callerOf(pc *middleware.PlatformContext) (caller, bool) {
	if pc == nil || pc.AuthType == middleware.AuthTypeAnonymous {
		return caller{}, false
	}
	c := caller{id: pc.UserID, email: strings.TrimSpace(pc.UserEmail)}
	switch {
	case c.email != "":
		c.key = strings.ToLower(c.email)
	case c.id != "":
		c.key = c.id
	default:
		return caller{}, false
	}
	return c, true
}

// Build returns the caller's digest, or nil when there is nothing to brief them
// on. Delivering a digest advances the caller's watermark, so what one session
// is told is not repeated to the next; a digest assembled from a failed query is
// still returned but does NOT advance the watermark, so a transient database
// error delays a notice rather than swallowing it.
//
// Orientation must not fail because a notice could not be assembled, so every
// error here is logged and swallowed.
func (h *Handle) Build(ctx context.Context, pc *middleware.PlatformContext) *Digest {
	if h == nil {
		return nil
	}
	c, ok := callerOf(pc)
	if !ok {
		return nil
	}
	now := h.now()
	since, err := h.since(ctx, c.key, now)
	if err != nil {
		slog.WarnContext(ctx, "platform_info: notice watermark unavailable", logKeyError, err)
		return nil
	}
	digest, complete := h.collect(ctx, c, since)
	if digest.empty() {
		return nil
	}
	if complete {
		if err := h.marks.Set(ctx, c.key, now); err != nil {
			slog.WarnContext(ctx, "platform_info: notice watermark not advanced", logKeyError, err)
		}
	}
	return digest
}

// since returns the instant this digest reports from: the caller's watermark,
// or the first-run window when they have never been briefed.
func (h *Handle) since(ctx context.Context, key string, now time.Time) (time.Time, error) {
	mark, err := h.marks.Get(ctx, key)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the caller's notice watermark: %w", err)
	}
	if mark == nil {
		return now.Add(-firstRunLookback), nil
	}
	return *mark, nil
}

// collect gathers both halves of the digest, reporting whether every query
// succeeded. A half that failed is left empty and its error logged; the caller
// withholds the watermark advance so the missing half is reported next session.
func (h *Handle) collect(ctx context.Context, c caller, since time.Time) (*Digest, bool) {
	digest := &Digest{Since: stamp(since)}
	complete := true

	feedback, total, err := h.feedback(ctx, c, since)
	if err != nil {
		slog.WarnContext(ctx, "platform_info: feedback notices unavailable", logKeyError, err)
		complete = false
	} else {
		digest.Feedback, digest.FeedbackTotal = feedback, total
	}

	shares, truncated, err := h.newShares(ctx, c, since)
	if err != nil {
		slog.WarnContext(ctx, "platform_info: share notices unavailable", logKeyError, err)
		complete = false
	} else {
		digest.NewShares, digest.NewSharesTruncated = shares, truncated
	}
	return digest, complete
}

// feedback returns the unresolved threads other people left on assets the
// caller owns, with activity since the watermark, plus how many such threads
// exist in total.
func (h *Handle) feedback(ctx context.Context, c caller, since time.Time) ([]FeedbackNotice, int, error) {
	names, ids, err := h.ownedAssets(ctx, c.id)
	if err != nil || len(ids) == 0 {
		return nil, 0, err
	}
	found, total, err := h.threads.ListThreads(ctx, threads.ThreadFilter{
		TargetAssetIDs: ids,
		Unresolved:     true,
		ActivityAfter:  &since,
		// The caller's own threads and their own replies are not feedback
		// awaiting them, so neither raises a notice.
		ExcludeAuthorID:    c.id,
		ExcludeAuthorEmail: c.email,
		Limit:              maxFeedbackNotices,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("listing feedback threads: %w", err)
	}
	out := make([]FeedbackNotice, 0, len(found))
	for _, t := range found {
		out = append(out, FeedbackNotice{
			ThreadID:       t.ID,
			Kind:           t.Kind,
			Status:         t.Status,
			Title:          t.Title,
			AuthorEmail:    t.AuthorEmail,
			AssetID:        t.AssetID,
			AssetName:      names[t.AssetID],
			AssetReference: reference(portaldomain.TargetTypeAsset, t.AssetID),
			LastActivityAt: stamp(t.LastEventAt),
		})
	}
	return out, total, nil
}

// ownedAssets returns the caller's assets as an id-to-name map and the id list
// the thread query is scoped to. Ownership is by user id, matching how the
// portal scopes every other owned-artifact view, so a caller without one owns
// nothing here.
func (h *Handle) ownedAssets(ctx context.Context, ownerID string) (names map[string]string, ids []string, err error) {
	if ownerID == "" {
		return nil, nil, nil
	}
	owned, total, listErr := h.assets.List(ctx, portaldomain.AssetFilter{OwnerID: ownerID, Limit: maxOwnedAssets})
	if listErr != nil {
		return nil, nil, fmt.Errorf("listing owned assets: %w", listErr)
	}
	if total > len(owned) {
		slog.WarnContext(ctx, "notices: owned asset set truncated; feedback on the remainder is not reported",
			"owner", logsan.SanitizeForLog(ownerID), "total", total, "cap", maxOwnedAssets)
	}
	names = make(map[string]string, len(owned))
	ids = make([]string, 0, len(owned))
	for _, a := range owned {
		names[a.ID] = a.Name
		ids = append(ids, a.ID)
	}
	return names, ids, nil
}

// newShares returns the artifacts shared with the caller since the watermark,
// and whether more arrived than the digest names. It asks for one row past the
// cap: the query is bounded rather than counted, and an overflow row is the
// cheapest honest answer to "is this list everything".
func (h *Handle) newShares(ctx context.Context, c caller, since time.Time) ([]ShareNotice, bool, error) {
	refs, err := h.shares.ListSharedWithUserSince(ctx, c.id, c.email, since, maxShareNotices+1)
	if err != nil {
		return nil, false, fmt.Errorf("listing new shares: %w", err)
	}
	truncated := len(refs) > maxShareNotices
	if truncated {
		refs = refs[:maxShareNotices]
	}
	out := make([]ShareNotice, 0, len(refs))
	for _, r := range refs {
		out = append(out, ShareNotice{
			Kind:       r.TargetType,
			ID:         r.TargetID,
			Name:       r.TargetName,
			Reference:  reference(r.TargetType, r.TargetID),
			SharedBy:   r.SharedBy,
			SharedAt:   stamp(r.SharedAt),
			Permission: string(r.Permission),
		})
	}
	return out, truncated, nil
}

// reference builds the platform reference string that dereferences an artifact
// through `fetch`. The share store's target types are exactly the reference
// scheme's type segment (asset, collection, prompt), so no mapping is needed.
func reference(kind, id string) string {
	return "mcp:" + kind + ":" + id
}
