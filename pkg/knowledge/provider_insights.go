package knowledge

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/tableavail"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/query"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// SourceInsights is the provenance label for insight-provider hits.
const SourceInsights = "insights"

// insightSource is the slice of the insight store the provider needs: the
// relevance search (text path) plus the entity-keyed list (entity path). It
// matches knowledgekit.SearchableInsightStore; declared locally so the provider
// depends on the capability and tests can supply a fake.
type insightSource interface {
	Search(ctx context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error)
	List(ctx context.Context, filter knowledgekit.InsightFilter) ([]knowledgekit.Insight, int, error)
	// Get reads one insight by id (the read half of search: a hit's reference
	// dereferenced to the full insight).
	Get(ctx context.Context, id string) (*knowledgekit.Insight, error)
}

// InsightsProvider exposes captured domain knowledge (insights) to the router.
//
// Insights are knowledge-dimension memory rows owned by their capturer
// (insight.captured_by). The store scopes to the knowledge dimension, so this
// provider covers exactly the records the MemoryProvider skips.
//
// It searches two arms and merges them (#980 B2):
//
//   - the owner arm, the caller's own insights at any review status. This is
//     personal recall and is unchanged.
//   - the shared arm, every capturer's insights at StatusApplied. An applied
//     insight has been reviewed and written to a canonical sink with a
//     changeset, which is the act that turns one person's capture into
//     organization knowledge. Before this, such a fact reached other people
//     only if its sink happened to be a knowledge page (PagesProvider is
//     unscoped) or if a tool result named the entity it hangs off, which is
//     what the benchmark measured as a cross-identity transfer gap.
//
// Statuses short of applied stay private to their capturer: pending and
// approved are unpublished personal captures, and rejected, superseded and
// rolled-back knowledge is retracted (isLiveInsightStatus).
//
// Scope stays ScopePerUser even though the shared arm returns other people's
// records. ScopePerUser is what makes the Router refuse this provider to an
// anonymous caller, and the search toolkit builds an anonymous caller whenever
// a request carries no platform context, so a public share viewer must not be
// able to reach applied organization knowledge through it.
type InsightsProvider struct {
	store    insightSource
	verifier EntityVerifier
}

// EntityVerifier resolves entity URNs to the queryable table behind them, so a
// delivered claim can name the one query that would settle it. It is declared
// here (rather than imported) so the provider depends on the capability, not on
// the query provider or the cache in front of it.
type EntityVerifier interface {
	// Verifiables returns the queryable identity of each URN that resolves,
	// keyed by URN. URNs that do not resolve are absent from the result.
	Verifiables(ctx context.Context, urns []string) map[string]query.Verifiable
}

// NewInsightsProvider builds the insights provider over a searchable insight
// store.
func NewInsightsProvider(store insightSource) *InsightsProvider {
	return &InsightsProvider{store: store}
}

// SetVerifier wires the resolver that marks a delivered insight as checkable
// (#1220). Nil (the default, and what a deployment with no query provider or an
// operator opt-out gets) leaves every delivered payload exactly as it was.
func (p *InsightsProvider) SetVerifier(v EntityVerifier) { p.verifier = v }

// Name returns the provenance label.
func (*InsightsProvider) Name() string { return SourceInsights }

// Scope marks this provider per-user: it requires an identified caller. See the
// type doc for why the shared arm does not make it ScopeShared.
func (*InsightsProvider) Scope() Scope { return ScopePerUser }

// Search returns the caller's captured insights plus the organization's applied
// ones. It serves both query shapes: an exact entity-keyed lookup on EntityURNs
// (insights linked to the requested datasets, lineage-expanded by the Router)
// and a relevance search on Intent. Results from every path are merged and
// de-duplicated by insight id. Each hit carries the insight's review status,
// capturer and linked entity URNs as provenance. It fails closed on a missing
// caller email rather than searching across all users.
//
// The owner arm runs first so that when a caller's own insight is also applied,
// the hit kept is the one read under their own identity.
func (p *InsightsProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.Caller.Email == "" {
		return nil, nil
	}

	seen := make(map[string]bool)
	hits, err := p.searchArm(ctx, q, seen, false)
	if err != nil {
		return nil, err
	}

	if sharedArmApplies(q.Status) {
		shared, armErr := p.searchArm(ctx, q, seen, true)
		if armErr != nil {
			return nil, armErr
		}
		hits = trimToLimit(append(hits, shared...), q.Limit)
	}
	return p.markVerifiable(ctx, hits, q.Caller), nil
}

// markVerifiable names, on each hit whose entity resolves, the table one query
// would settle its claim against (#1220). The whole page is resolved in one pass
// so an entity claimed by several insights costs one lookup, and the marker is
// applied after the arms merge so a hit is marked exactly once however it was
// found. A hit whose entities do not resolve is returned untouched.
func (p *InsightsProvider) markVerifiable(ctx context.Context, hits []Hit, caller Caller) []Hit {
	if p.verifier == nil || len(hits) == 0 {
		return hits
	}

	sets := make([][]string, 0, len(hits))
	for i := range hits {
		sets = append(sets, hits[i].EntityURNs)
	}
	urns := reachableURNs(tableavail.Distinct(sets...), caller)
	if len(urns) == 0 {
		return hits
	}
	resolved := p.verifier.Verifiables(ctx, urns)
	if len(resolved) == 0 {
		return hits
	}

	for i := range hits {
		hits[i].Verifiable = firstVerifiable(hits[i].EntityURNs, resolved, caller)
	}
	return hits
}

// reachableURNs drops the entities the caller's persona may not reach, before
// any of them is looked up.
//
// An insight is not connection-scoped — knowing a colleague's conclusion about a
// warehouse you cannot query is the point of shared knowledge — but the marker
// is topology: it names a connection and asserts the entity is reachable on it,
// which is exactly what the catalog and connections arms withhold from a persona
// whose rules exclude that connection (#1108). Filtering here also keeps the
// resolution from issuing a describe against a connection the caller's persona
// could not have queried.
func reachableURNs(urns []string, caller Caller) []string {
	out := make([]string, 0, len(urns))
	for _, urn := range urns {
		if caller.allowsURN(urn) {
			out = append(out, urn)
		}
	}
	return out
}

// firstVerifiable returns the queryable identity of the first URN that resolved
// to a connection the caller may reach, in the order the record carries its
// entities. A record linked to several entities names one table to check rather
// than a list to choose from: the first linked entity is the record's own
// primary subject, and a claim that needs more than one table to settle is not
// the checkable shape this marker is for.
//
// The connection is re-checked here because the URN filter is a different
// question: a URN maps to every connection of its platform and passes when the
// persona may reach ANY of them, while the resolved answer names the one
// connection the marker would actually disclose.
func firstVerifiable(urns []string, resolved map[string]query.Verifiable, caller Caller) *query.Verifiable {
	for _, urn := range urns {
		v, ok := resolved[urn]
		if !ok || !caller.allowsConnection(v.Connection) {
			continue
		}
		return &v
	}
	return nil
}

// trimToLimit caps the merged arms at the candidate limit the Router asked this
// provider for. Each arm queries the store with that limit, so returning both
// unmerged would hand back up to twice as many candidates as any other source,
// overstating the insights coverage count and giving the allocator a deeper pool
// for this one source than the caller asked for. The owner arm is kept first, so
// what a trim drops is the organization's copy rather than the caller's own.
func trimToLimit(hits []Hit, limit int) []Hit {
	if limit <= 0 || len(hits) <= limit {
		return hits
	}
	return hits[:limit]
}

// sharedArmApplies reports whether the shared arm can contribute to a query.
// That arm only ever returns applied insights, so an explicit request for any
// other status is already answered in full by the owner arm; running it anyway
// would return records the caller filtered out.
func sharedArmApplies(status string) bool {
	return status == "" || status == knowledgekit.StatusApplied
}

// searchArm runs both query shapes under one owner scope and merges them.
// shared selects the organization-wide arm over the caller's own records.
func (p *InsightsProvider) searchArm(ctx context.Context, q Query, seen map[string]bool, shared bool) ([]Hit, error) {
	entityHits, err := p.searchByEntity(ctx, q, seen, shared)
	if err != nil {
		return nil, err
	}
	textHits, err := p.searchByText(ctx, q, seen, shared)
	if err != nil {
		return nil, err
	}
	return append(entityHits, textHits...), nil
}

// searchByEntity returns insights linked to the query's entity URNs (already
// lineage-expanded by the Router). It reuses the entity-keyed List path that
// memory_manage(filter_entity_urn=...) relies on, scoped to the knowledge
// dimension by the store and to either the caller (owner arm) or the applied
// set (shared arm). Already-seen insights are skipped; when no explicit status
// was requested, rejected/superseded/rolled-back insights are dropped so a
// "what do we know" lookup never surfaces retracted knowledge.
func (p *InsightsProvider) searchByEntity(ctx context.Context, q Query, seen map[string]bool, shared bool) ([]Hit, error) {
	if len(q.EntityURNs) == 0 {
		return nil, nil
	}

	var hits []Hit
	for _, urn := range q.EntityURNs {
		insights, _, err := p.store.List(ctx, knowledgekit.InsightFilter{
			EntityURN:  urn,
			CapturedBy: ownerScope(q, shared),
			Status:     q.Status,
			Shared:     shared,
			Limit:      q.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("insight entity lookup: %w", err)
		}
		for i := range insights {
			if seen[insights[i].ID] {
				continue
			}
			if q.Status == "" && !isLiveInsightStatus(insights[i].Status) {
				continue
			}
			seen[insights[i].ID] = true
			hits = append(hits, insightHit(insights[i], entityMatchScore))
		}
	}
	return hits, nil
}

// ownerScope returns the capturer the arm scopes on: the caller for the owner
// arm, and nobody for the shared arm, which the store re-scopes to the applied
// set of every capturer. It is passed explicitly rather than left empty so the
// owner arm can never lose its predicate by omission.
func ownerScope(q Query, shared bool) string {
	if shared {
		return ""
	}
	return q.Caller.Email
}

// searchByText returns insights ranked by relevance to the intent, optionally
// filtered by review status, from either the caller's own set (owner arm) or
// the applied set (shared arm). Already-seen insights (recalled on the entity
// path, or already returned by the owner arm) are skipped. A query with no
// intent yields nothing here.
func (p *InsightsProvider) searchByText(ctx context.Context, q Query, seen map[string]bool, shared bool) ([]Hit, error) {
	if q.Intent == "" {
		return nil, nil
	}

	scored, err := p.store.Search(ctx, knowledgekit.InsightSearchQuery{
		QueryText:  q.Intent,
		Embedding:  q.Embedding,
		CapturedBy: ownerScope(q, shared),
		Status:     q.Status,
		Shared:     shared,
		Limit:      q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("insight search: %w", err)
	}

	hits := make([]Hit, 0, len(scored))
	for i := range scored {
		if seen[scored[i].Insight.ID] {
			continue
		}
		// Same retraction as the entity path: with no explicit status requested, a
		// rejected/superseded/rolled-back insight is no longer in force and must not
		// surface in a "what do we know" lookup (#684).
		if q.Status == "" && !isLiveInsightStatus(scored[i].Insight.Status) {
			continue
		}
		seen[scored[i].Insight.ID] = true
		hits = append(hits, insightHit(scored[i].Insight, scored[i].Score))
	}
	return hits, nil
}

// insightHit maps an insight to a knowledge hit, carrying its review status and
// linked entity URNs as provenance, plus the canonical mcp:insight:<id> reference
// so an agent can read the full insight with fetch and cite it on a page (#699).
func insightHit(in knowledgekit.Insight, score float64) Hit {
	return Hit{
		Text:       in.InsightText,
		Source:     SourceInsights,
		Ref:        in.ID,
		Score:      score,
		Status:     in.Status,
		EntityURNs: in.EntityURNs,
		CapturedBy: in.CapturedBy,
		Reference:  knowledgepage.InsightRef(in.ID),
	}
}

// Fetch dereferences an mcp:insight:<id> reference to the full insight (#699),
// following the AssetsProvider precedent. The read is scoped to exactly what
// Search could have returned, so fetch never reveals an insight the caller could
// not have found and never refuses a reference search handed out: the caller's
// own insights at any status (the owner arm), plus any capturer's applied
// insights (the shared arm). A non-owner's unapplied insight, a missing id, or
// an anonymous caller is ErrNotFound. Within the owner arm it does NOT
// additionally gate on review status: Search retracts non-live insights only
// from the default (no-status) discovery path, while an explicit status query
// surfaces them, so a caller can search any of their own insights by status and
// fetch must dereference any reference search hands out. The knowledge-dimension
// scope is enforced by the store adapter's Get, so a reference that names a
// non-knowledge memory record resolves to not-found here.
func (p *InsightsProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	parsed, err := knowledgepage.ParseEntityRef(ref)
	if err != nil || parsed.TargetType != knowledgepage.RefTargetInsight {
		return nil, false, nil //nolint:nilerr // a non-insight reference is a decline, not a failure
	}
	if caller.Email == "" {
		return nil, true, ErrNotFound
	}
	in, err := p.store.Get(ctx, parsed.InsightID)
	if err != nil {
		// Insights are memory_records behind the adapter, so a missing id (or a
		// non-knowledge record) surfaces memory.ErrRecordNotFound (wrapped), NOT
		// sql.ErrNoRows; a stale citation is a clean not-found.
		if errors.Is(err, memory.ErrRecordNotFound) {
			return nil, true, ErrNotFound
		}
		return nil, true, fmt.Errorf("getting insight %s: %w", parsed.InsightID, err)
	}
	if in == nil || !readableBy(*in, caller) {
		return nil, true, ErrNotFound
	}
	return &Document{
		Reference:  ref,
		Source:     SourceInsights,
		Body:       in.InsightText,
		Content:    in,
		EntityURNs: in.EntityURNs,
		Verifiable: p.verifiableFor(ctx, in.EntityURNs, caller),
	}, true, nil
}

// verifiableFor resolves one record's entities to the table its claim could be
// settled against, so a fetched insight carries the same marker its search hit
// did (#1220) — including the same persona connection boundary, since a fetch
// must never disclose topology a search would have withheld.
func (p *InsightsProvider) verifiableFor(ctx context.Context, urns []string, caller Caller) *query.Verifiable {
	if p.verifier == nil || len(urns) == 0 {
		return nil
	}
	reachable := reachableURNs(tableavail.Distinct(urns), caller)
	if len(reachable) == 0 {
		return nil
	}
	return firstVerifiable(urns, p.verifier.Verifiables(ctx, reachable), caller)
}

// readableBy reports whether a caller may read an insight: their own at any
// status, or anyone's once it is applied. It is the read-side statement of the
// two Search arms, so the set fetch dereferences and the set search returns
// cannot drift apart.
func readableBy(in knowledgekit.Insight, caller Caller) bool {
	return in.CapturedBy == caller.Email || in.Status == knowledgekit.StatusApplied
}

// isLiveInsightStatus reports whether an insight status represents knowledge
// still in force. Rejected, superseded, and rolled-back insights are retracted
// and must not surface on either unfiltered discovery path (entity or text).
func isLiveInsightStatus(status string) bool {
	switch status {
	case knowledgekit.StatusRejected, knowledgekit.StatusSuperseded, knowledgekit.StatusRolledBack:
		return false
	default:
		return true
	}
}
