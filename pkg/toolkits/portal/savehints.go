package portal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetsimilar"
	"github.com/txn2/mcp-data-platform/internal/portal/assettags"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// What save_asset reports beside the asset it saved (#2508): the existing tags
// close to the ones it was given, and the assets the caller can open that look
// like the same work. Both are read after the save is stored, within
// hintTimeout, and neither refuses nor holds a save: a lookup that fails or
// runs out of time reports nothing.

// hintTimeout bounds the lookups a save reports, embedding the new asset's
// text among them.
const hintTimeout = 2 * time.Second

// visibleTagLimit bounds the tags read to find the variants of a given one.
const visibleTagLimit = 5000

// TagLister lists the tags on the assets a person can open.
type TagLister interface {
	List(ctx context.Context, q assettags.Query) (assettags.Page, error)
}

// SimilarFinder finds the assets a person can open closest to a vector.
type SimilarFinder interface {
	Nearest(ctx context.Context, q assetsimilar.NearestQuery) ([]assetsimilar.Match, error)
}

// saveHints holds what the save-time lookups read.
type saveHints struct {
	tags    TagLister
	similar SimilarFinder
}

// SetSaveHints gives save_asset the tag list and the closeness search it
// reports existing_tags and similar_assets from. Unset, a save reports
// neither.
func (t *Toolkit) SetSaveHints(tags TagLister, similar SimilarFinder) {
	t.hints = &saveHints{tags: tags, similar: similar}
}

// addSaveHints fills out's existing_tags and similar_assets for the asset just
// saved, and says what they mean in its message.
func (t *Toolkit) addSaveHints(ctx context.Context, asset *portal.Asset, out *saveAssetOutput) {
	reach := callerReach(ctx, string(portaldomain.ScopeAll), portaldomain.ScopeAll)
	if t.hints == nil || reach == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, hintTimeout)
	defer cancel()
	out.ExistingTags = t.existingTags(ctx, reach, asset.Tags)
	out.SimilarAssets = t.similarAssets(ctx, reach, asset)
	out.Message += hintNotice(out.ExistingTags, out.SimilarAssets)
}

// existingTags is the tags on the assets the caller can open that are a case
// or spelling variant of one the save was given.
func (t *Toolkit) existingTags(ctx context.Context, reach *portaldomain.Reach, given []string) []assettags.Variant {
	if t.hints.tags == nil || len(given) == 0 {
		return nil
	}
	page, err := t.hints.tags.List(ctx, assettags.Query{Reach: reach, Limit: visibleTagLimit})
	if err != nil {
		slog.WarnContext(ctx, "save_asset: existing tags not read", "error", logsan.SanitizeForLog(err.Error())) // #nosec G706 -- structured slog call; error sanitized
		return nil
	}
	return assettags.Variants(given, page.Tags, assettags.DefaultPerTag)
}

// similarAssets is the assets the caller can open, other than the one just
// saved, whose stored embedding sits within assetsimilar.NearDuplicate of the
// new asset's text: the same name, description and tags its own embedding
// will be made from.
func (t *Toolkit) similarAssets(ctx context.Context, reach *portaldomain.Reach, asset *portal.Asset) []assetsimilar.Match {
	if t.hints.similar == nil || !embedding.IsConfigured(t.embedder) {
		return nil
	}
	vec := embedding.EmbedForSearch(ctx, t.embedder, portal.AssetIndexText(asset.Name, asset.Description, asset.Tags))
	if len(vec) == 0 {
		return nil
	}
	matches, err := t.hints.similar.Nearest(ctx, assetsimilar.NearestQuery{
		Vector: vec, Reach: reach, Exclude: []string{asset.ID}, Limit: assetsimilar.DefaultLimit,
	})
	if err != nil {
		slog.WarnContext(ctx, "save_asset: similar assets not read", "error", logsan.SanitizeForLog(err.Error())) // #nosec G706 -- structured slog call; error sanitized
		return nil
	}
	return matches
}

// hintNotice is what the save's message adds for what the lookups found.
func hintNotice(tags []assettags.Variant, similar []assetsimilar.Match) string {
	var b strings.Builder
	if len(tags) > 0 {
		parts := make([]string, 0, len(tags))
		for _, v := range tags {
			parts = append(parts, fmt.Sprintf("%q (for %q, on %d assets)", v.Tag, v.CloseTo, v.Count))
		}
		_, _ = b.WriteString(" Existing tags close to the ones given: " + strings.Join(parts, ", ") +
			". Reuse the existing tag with manage_asset action=update unless the new one is meant to differ.")
	}
	if len(similar) > 0 {
		parts := make([]string, 0, len(similar))
		for _, m := range similar {
			parts = append(parts, fmt.Sprintf("%q (%s, owned by %s)", m.Name, m.AssetID, m.OwnerEmail))
		}
		_, _ = b.WriteString(" This looks like the same work as " + strings.Join(parts, ", ") +
			": tell the person, and offer to save it as a new version of that asset instead (manage_asset action=update on it, then delete this one).")
	}
	return b.String()
}
