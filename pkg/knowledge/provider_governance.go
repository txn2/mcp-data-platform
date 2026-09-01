package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// SourceGovernance is the provenance label for governance-vocabulary hits.
const SourceGovernance = "governance"

// URN forms of the three governance vocabularies. The governance source owns
// exactly these prefixes for fetch; the catalog owns urn:li:dataset: and the
// context-documents source owns urn:li:document:, so no two urn:li: sources
// contend for a reference.
const (
	glossaryTermPrefix = "urn:li:glossaryTerm:"
	tagPrefix          = "urn:li:tag:"
	domainPrefix       = "urn:li:domain:"
)

const (
	// vocabularyPageLimit is the page asked for when listing a vocabulary to match
	// a URN against, for the two kinds that have no by-URN read. The adapter
	// clamps it to its own maximum; a URN past that page stays unresolved and
	// fetch reports a clean not-found, the same bound the portal's label resolver
	// carries (#1159).
	vocabularyPageLimit = 200

	// carrierLimit bounds the datasets listed under one governance entity. A
	// widely applied tag can be on thousands of datasets, and a fetch is a read of
	// the entity, not an export of its membership; the catalog search itself is
	// the unbounded path (filter by the tag, page the results).
	carrierLimit = 25

	// listQuery is the relevance query a carrier listing uses: "*", not the empty
	// string. The query reaches DataHub's searchAcrossEntities verbatim (mcp-datahub
	// buildBaseSearchInput), where "" is a query that matches nothing rather than a
	// wildcard, so an empty one would silently report every governance entity as
	// carried by no dataset. It is the same shape the portal's shipped tag, domain,
	// and glossary usage lists send (ui/src/api/portal/datahub.ts: `q=*&tags=...`).
	listQuery = "*"
)

// governanceReader is the catalog capability the governance provider needs: a
// relevance search per vocabulary, a resolve per vocabulary, and the dataset
// search that lists what carries an entry. It matches semantic.GovernanceReader;
// declared locally so the provider depends on the capability and tests can supply
// a fake.
type governanceReader interface {
	SearchGlossaryTerms(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error)
	GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error)
	SearchTags(ctx context.Context, query string, limit int) ([]semantic.EntityRef, error)
	ListDomains(ctx context.Context) ([]semantic.EntityRef, error)
	SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error)
}

// GovernanceProvider makes DataHub's glossary terms, tags, and domains
// first-class in the knowledge read path (#1160). Before it, the three were only
// ever attributes of a dataset: asking what a business term means returned the
// datasets tagged with it rather than the term and its definition, and a
// knowledge page could legitimately cite a urn:li:glossaryTerm: reference that
// fetch had no owner for.
//
// It is a sibling of the catalog source rather than an arm of it. The two answer
// different questions from different corpora — "which datasets are about X"
// versus "what does X mean here" — and the allocator balances the display budget
// per source, so folding a vocabulary of tens of entries into the dataset arm
// would let a broad dataset match crowd out the definition the caller asked for.
// Separate ownership also keeps fetch's reference partition explicit: one prefix,
// one owner.
//
// It responds to the text path only. A governance URN handed to search is a
// reference, and fetch is the verb that dereferences one; an entity arm here
// would re-read the same dataset contexts the catalog arm already reads to answer
// what a dataset carries, and a fetched dataset already carries its terms, tags,
// and domain.
//
// It is shared: the vocabulary holds no per-user records. Its entities are still
// put through the caller's connection boundary (#1108), which for a governance
// URN resolves to "visible": these URNs carry no dataPlatform segment, so no
// connection can be attributed to them, and the documented rule for an
// unattributable URN is that it stays visible — the mapping failed, not the
// permission check. The datasets listed UNDER an entity do carry a platform, and
// they are filtered like any other catalog hit.
type GovernanceProvider struct {
	reader governanceReader
}

// NewGovernanceProvider builds the governance provider over a governance reader.
func NewGovernanceProvider(reader governanceReader) *GovernanceProvider {
	return &GovernanceProvider{reader: reader}
}

// Name returns the provenance label.
func (*GovernanceProvider) Name() string { return SourceGovernance }

// Scope marks the governance vocabulary shared (global, always queried).
func (*GovernanceProvider) Scope() Scope { return ScopeShared }

// GovernanceEntity is the full content of a fetched governance entity: the
// vocabulary entry itself plus the datasets that carry it, which is what makes
// the definition actionable ("Net Revenue means this, and these three tables
// report it").
type GovernanceEntity struct {
	URN  string `json:"urn"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Description is a term's definition or a tag's/domain's description.
	Description string `json:"description,omitempty"`
	// Datasets are the catalog datasets carrying this entity, bounded by
	// carrierLimit and filtered to the caller's connection boundary.
	Datasets []GovernanceDataset `json:"datasets,omitempty"`
	// MoreDatasets reports that the list is not known to hold every carrier:
	// either the catalog counted more, or it could not count at all. Filter the
	// catalog search by the entity to page the full membership.
	MoreDatasets bool `json:"more_datasets,omitempty"`
	// DatasetsWithheld counts the carrying datasets the caller's connection
	// boundary removed, and Notice explains it. A short list with no explanation is
	// indistinguishable from an entity nothing carries.
	DatasetsWithheld int    `json:"datasets_withheld,omitempty"`
	Notice           string `json:"notice,omitempty"`
}

// GovernanceDataset is one catalog dataset carrying a governance entity, reduced
// to what a reader needs to recognize it and fetch it in turn.
type GovernanceDataset struct {
	URN  string `json:"urn"`
	Name string `json:"name"`
}

// governanceKind is one governance vocabulary: how it is labeled, the URN form
// that identifies it, the two ways its entries are obtained, how one entry is
// resolved from a URN, and the catalog filter that lists the datasets carrying an
// entry.
//
// The kinds differ in exactly one way that matters, and it is an upstream
// asymmetry rather than a design choice: a term has a by-URN read and a name
// search, a tag has a name search only, and a domain has neither. Holding that
// per kind keeps search and fetch from growing a switch each; a nil search or a
// nil resolve says "upstream offers none", and the shared paths fall back to the
// enumeration every kind does have.
type governanceKind struct {
	// kind is the machine label on a fetched entity ("glossary_term").
	kind string
	// label names the vocabulary in a hit's heading ("glossary term").
	label string
	// prefix is the URN form this kind owns.
	prefix string
	// search asks upstream for the entries relevant to intent, already ranked.
	// Nil when the vocabulary has no upstream search (domains).
	search func(ctx context.Context, r governanceReader, intent string, limit int) ([]semantic.EntityRef, error)
	// enumerate lists the vocabulary. Every kind has one; it backs both the
	// local-ranking fallback and the URN resolve for the kinds with no by-URN read.
	enumerate func(ctx context.Context, r governanceReader, limit int) ([]semantic.EntityRef, error)
	// resolve returns the entry named by urn, or nil when the vocabulary has no
	// such entry. Nil when upstream has no by-URN read, which sends the resolve
	// through enumerate instead.
	resolve func(ctx context.Context, r governanceReader, urn string) (*semantic.EntityRef, error)
	// carriers is the catalog filter listing the datasets that carry urn.
	carriers func(urn string, limit int) semantic.SearchFilter
}

// governanceKinds is the vocabulary table, in the order a merged result set
// interleaves them: a business term is the answer to "what does X mean", so it
// leads, with the two classification vocabularies behind it.
var governanceKinds = []governanceKind{
	{
		kind:   "glossary_term",
		label:  "glossary term",
		prefix: glossaryTermPrefix,
		search: func(ctx context.Context, r governanceReader, intent string, limit int) ([]semantic.EntityRef, error) {
			return r.SearchGlossaryTerms(ctx, intent, limit)
		},
		enumerate: func(ctx context.Context, r governanceReader, limit int) ([]semantic.EntityRef, error) {
			return r.SearchGlossaryTerms(ctx, "", limit)
		},
		resolve: resolveGlossaryTerm,
		carriers: func(urn string, limit int) semantic.SearchFilter {
			return semantic.SearchFilter{
				Query:   listQuery,
				Filters: []semantic.FieldFilter{{Field: semantic.FilterFieldGlossaryTerms, Values: []string{urn}}},
				Limit:   limit,
			}
		},
	},
	{
		kind:   "tag",
		label:  "tag",
		prefix: tagPrefix,
		search: func(ctx context.Context, r governanceReader, intent string, limit int) ([]semantic.EntityRef, error) {
			return r.SearchTags(ctx, intent, limit)
		},
		enumerate: func(ctx context.Context, r governanceReader, limit int) ([]semantic.EntityRef, error) {
			return r.SearchTags(ctx, "", limit)
		},
		carriers: func(urn string, limit int) semantic.SearchFilter {
			return semantic.SearchFilter{Query: listQuery, Tags: []string{urn}, Limit: limit}
		},
	},
	{
		kind:   "domain",
		label:  "domain",
		prefix: domainPrefix,
		enumerate: func(ctx context.Context, r governanceReader, _ int) ([]semantic.EntityRef, error) {
			return r.ListDomains(ctx)
		},
		carriers: func(urn string, limit int) semantic.SearchFilter {
			return semantic.SearchFilter{Query: listQuery, Domain: urn, Limit: limit}
		},
	},
}

// vocabularyEntries returns the entries of one vocabulary relevant to intent.
//
// Upstream's own search leads where there is one: it ranks against DataHub's
// index and is not bounded by an enumeration page, so a large glossary stays
// fully reachable. An empty result falls back to enumerating the vocabulary and
// ranking it here, because search receives a natural-language intent while these
// two upstream searches match a NAME: "what does net revenue mean here" is a
// question, not a label, and a source that answers it only when the caller types
// the term exactly would not be answering it. Domains have no upstream search at
// all and take the fallback path directly.
//
// The enumeration asks for the vocabulary page, not the display budget: ranking
// happens here, so narrowing the read to the few entries that will be shown would
// rank an arbitrary slice of the vocabulary rather than the vocabulary.
//
// A failed read fails the vocabulary, with one exception: a fallback enumeration
// that fails AFTER a successful but empty upstream search does not, because the
// vocabulary already answered — it answered "nothing" — and the fallback was only
// trying to do better. Where there is no upstream search, the enumeration IS the
// vocabulary's read and its failure is the vocabulary's.
func vocabularyEntries(
	ctx context.Context, r governanceReader, kind governanceKind, intent string, limit int,
) ([]semantic.EntityRef, error) {
	if kind.search == nil {
		listed, err := kind.enumerate(ctx, r, vocabularyPageLimit)
		if err != nil {
			return nil, err
		}
		return rankRefs(listed, intent, limit), nil
	}
	refs, err := kind.search(ctx, r, intent, limit)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		return refs, nil
	}
	listed, err := kind.enumerate(ctx, r, vocabularyPageLimit)
	if err != nil {
		slog.Debug("governance vocabulary enumeration skipped", "kind", kind.kind, "error", err)
		return nil, nil //nolint:nilerr // the vocabulary already answered; the fallback was best-effort
	}
	return rankRefs(listed, intent, limit), nil
}

// resolveEntry returns the vocabulary entry named by urn: through upstream's
// by-URN read where there is one, and otherwise by listing the vocabulary and
// matching, which is the only route a tag or a domain has. A URN the vocabulary
// holds no entry for is ErrNotFound, the same sentinel the fetch contract uses,
// so the miss travels as a value rather than as an ambiguous nil pair.
func resolveEntry(ctx context.Context, r governanceReader, kind governanceKind, urn string) (*semantic.EntityRef, error) {
	if kind.resolve != nil {
		return kind.resolve(ctx, r, urn)
	}
	listed, err := kind.enumerate(ctx, r, vocabularyPageLimit)
	if err != nil {
		// Unlike a by-URN read, a failing LISTING is unambiguously a failure and not
		// a miss: reporting it as not-found would tell a caller their tag was retired
		// when DataHub was merely unreachable.
		return nil, err
	}
	for i := range listed {
		if listed[i].URN == urn {
			return &listed[i], nil
		}
	}
	return nil, ErrNotFound
}

// resolveGlossaryTerm reads one term by URN, the only by-URN read any governance
// vocabulary has. A read error is reported as a miss (ErrNotFound) rather than a
// failure: as with a dataset, DataHub reports a missing or deleted glossary term
// as an error instead of an empty result, so a stale term citation must be a
// clean not-found rather than a hard tool failure.
func resolveGlossaryTerm(ctx context.Context, r governanceReader, urn string) (*semantic.EntityRef, error) {
	term, err := r.GetGlossaryTerm(ctx, urn)
	if err != nil {
		slog.Debug("glossary term lookup miss", "urn", urn, "error", err)
		return nil, ErrNotFound
	}
	if term == nil || term.URN == "" {
		return nil, ErrNotFound
	}
	return &semantic.EntityRef{URN: term.URN, Name: term.Name, Description: term.Description}, nil
}

// governanceKindFor returns the vocabulary owning a URN form, ok=false for every
// other reference.
func governanceKindFor(ref string) (governanceKind, bool) {
	for _, k := range governanceKinds {
		if strings.HasPrefix(ref, k.prefix) {
			return k, true
		}
	}
	return governanceKind{}, false
}

// Search returns the governance entries relevant to the intent, ranked and
// interleaved across the three vocabularies so one cannot crowd out the others.
// A query with no intent yields nothing: a vocabulary entry is found by what it
// is called and what it means.
//
// The three vocabularies are independent upstream reads, so they run
// concurrently: the arm costs the slowest vocabulary rather than the sum of all
// three (a vocabulary is one read, or two when its upstream search comes back
// empty and the local-ranking fallback runs). A vocabulary that fails is logged
// and dropped, so losing one costs its recall rather than the whole source; the
// source is blanked (the error propagates) only when EVERY vocabulary failed,
// which means the catalog itself is unhealthy.
func (p *GovernanceProvider) Search(ctx context.Context, q Query) ([]Hit, error) {
	if strings.TrimSpace(q.Intent) == "" {
		return nil, nil
	}
	lists, err := p.rankedVocabularies(ctx, q)
	if err != nil {
		return nil, err
	}

	// The boundary is evaluated inside the merge, ahead of its truncation, so a
	// withheld candidate does not consume one of the limited slots and leave a
	// truncated source looking exhausted (#1585). It is evaluated at all even
	// though a governance URN is unattributable today: the rule belongs to the
	// boundary, not to an assumption about URN shapes here.
	withheld := 0
	candidates := mergeCandidates(q.Limit, func(urn string) bool {
		if !q.Caller.allowsURN(urn) {
			withheld++
			return false
		}
		return true
	}, lists...)
	n := len(candidates)
	hits := make([]Hit, 0, n)
	for i := range candidates {
		hits = append(hits, Hit{
			Text:       candidates[i].text,
			Source:     SourceGovernance,
			Ref:        candidates[i].urn,
			Score:      positionalScore(i, n),
			EntityURNs: []string{candidates[i].urn},
			// A DataHub reference is its URN verbatim (the canonical citable form).
			Reference: candidates[i].urn,
		})
	}
	q.Caller.withhold(withheld)
	return hits, nil
}

// rankedVocabularies runs every vocabulary's relevance search concurrently and
// returns one ranked candidate list per kind, in governanceKinds order. It
// returns an error only when every vocabulary failed.
func (p *GovernanceProvider) rankedVocabularies(ctx context.Context, q Query) ([][]catalogCandidate, error) {
	lists := make([][]catalogCandidate, len(governanceKinds))
	errs := make([]error, len(governanceKinds))

	// Every task returns nil: one vocabulary that cannot be read must not cancel
	// the reads of the others, so a failure is a missing list rather than an error.
	var g errgroup.Group
	for i, kind := range governanceKinds {
		g.Go(func() error {
			refs, err := vocabularyEntries(ctx, p.reader, kind, q.Intent, q.Limit)
			if err != nil {
				errs[i] = err
				slog.Debug("governance vocabulary search skipped", "kind", kind.kind, "error", err)
				return nil
			}
			lists[i] = governanceCandidates(kind, refs)
			return nil
		})
	}
	_ = g.Wait()

	if failed := collectErrors(errs); len(failed) == len(governanceKinds) {
		return nil, fmt.Errorf("governance vocabulary search: %w", errors.Join(failed...))
	}
	return lists, nil
}

// collectErrors gathers the failures out of a per-vocabulary slot array, so
// "every vocabulary failed" is a count rather than a flag each arm maintains.
func collectErrors(errs []error) []error {
	out := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}

// governanceCandidates renders a vocabulary's entries as ranked candidates,
// dropping entries with no URN (nothing to cite or fetch).
func governanceCandidates(kind governanceKind, refs []semantic.EntityRef) []catalogCandidate {
	out := make([]catalogCandidate, 0, len(refs))
	for i := range refs {
		if refs[i].URN == "" {
			continue
		}
		out = append(out, catalogCandidate{
			urn:  refs[i].URN,
			text: governanceText(kind, refs[i]),
		})
	}
	return out
}

// governanceText renders a vocabulary entry as a knowledge snippet: its name
// labeled with which vocabulary it belongs to, then its definition or
// description. The definition rides in the hit text deliberately — a term match
// answers "what does this mean" on the spot, without a follow-up fetch.
func governanceText(kind governanceKind, ref semantic.EntityRef) string {
	return catalogSnippet(governanceName(ref)+" ("+kind.label+")", ref.Description)
}

// governanceName is an entry's display name, falling back to its URN when the
// vocabulary returned none. The fallback is the URN in full rather than its last
// segment: DataHub generates a UUID key for an entity created without an explicit
// id, so a trimmed key would read as a name while naming nothing.
func governanceName(ref semantic.EntityRef) string {
	if name := strings.TrimSpace(ref.Name); name != "" {
		return name
	}
	return ref.URN
}

// rankRefs orders entries that arrive with no backend ranking by token overlap
// against the intent, drops the ones nothing matched, and caps the page. It is
// the domain vocabulary's ranking, and it uses the same lexical rule the
// connections source uses for the same reason: neither has an upstream search to
// rank it.
func rankRefs(refs []semantic.EntityRef, intent string, limit int) []semantic.EntityRef {
	tokens := strings.Fields(strings.ToLower(intent))
	if len(tokens) == 0 {
		return nil
	}
	type scored struct {
		ref   semantic.EntityRef
		score float64
	}
	matches := make([]scored, 0, len(refs))
	for _, ref := range refs {
		if s := tokenOverlapScore(ref.Name+" "+ref.Description, tokens); s > 0 {
			matches = append(matches, scored{ref: ref, score: s})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].ref.URN < matches[j].ref.URN
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]semantic.EntityRef, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.ref)
	}
	return out
}

// Fetch dereferences a urn:li:glossaryTerm:, urn:li:tag:, or urn:li:domain:
// reference to the vocabulary entry and the datasets that carry it (#1160). It
// owns only those three URN forms; any other reference is declined (owned=false).
// A URN of one of these forms that the vocabulary has no entry for is ErrNotFound,
// so a stale citation — the reference a knowledge page keeps after a steward
// retires the term it cited — is a clean answer rather than a tool failure.
//
// The entity itself carries no connection attribution (no dataPlatform segment),
// so the boundary leaves it visible, which is the documented rule for an
// unattributable URN. The datasets listed under it are a different matter: those
// are catalog hits, and each is put through the caller's boundary exactly as
// search would, with the removed count and the reason reported rather than the
// list silently shortening.
func (p *GovernanceProvider) Fetch(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	kind, ok := governanceKindFor(ref)
	if !ok {
		return nil, false, nil
	}
	entry, err := resolveEntry(ctx, p.reader, kind, ref)
	if err != nil {
		// Either ErrNotFound (the vocabulary has no such entry) or a real read
		// failure, both of which the fetch contract passes through unchanged.
		return nil, true, err
	}
	if !caller.allowsURN(ref) {
		return nil, true, ErrNotFound
	}

	entity := GovernanceEntity{
		URN:         ref,
		Kind:        kind.kind,
		Name:        governanceName(*entry),
		Description: entry.Description,
	}
	urns := p.attachCarriers(ctx, kind, ref, caller, &entity)
	return &Document{
		Reference:  ref,
		Source:     SourceGovernance,
		Title:      entity.Name,
		Body:       entity.Description,
		Content:    entity,
		EntityURNs: urns,
	}, true, nil
}

// searchCarriers runs a carrier search and reports the catalog's total match
// count alongside the page, so the caller can tell a bounded membership from an
// exhausted one — a page length equal to the limit is evidence of neither, since
// the catalog is free to return fewer rows than asked for (#1238). A reader that
// cannot count reports semantic.TotalUnknown.
//
// The capability is probed directly rather than through semantic.SearchTablesCounted
// because the reader is a governanceReader, not a semantic.Provider: it arrives
// already resolved to the innermost implementation (semantic.GovernanceReaderFrom),
// so there is no decorator chain left to walk.
func (p *GovernanceProvider) searchCarriers(
	ctx context.Context, filter semantic.SearchFilter,
) (results []semantic.TableSearchResult, total int, err error) {
	if mc, ok := p.reader.(semantic.TableMatchCounter); ok {
		results, total, err = mc.SearchTablesCounted(ctx, filter)
		if err != nil {
			return nil, semantic.TotalUnknown, fmt.Errorf("counted carrier search: %w", err)
		}
		return results, total, nil
	}
	results, err = p.reader.SearchTables(ctx, filter)
	if err != nil {
		return nil, semantic.TotalUnknown, fmt.Errorf("carrier search: %w", err)
	}
	return results, semantic.TotalUnknown, nil
}

// attachCarriers lists the datasets carrying a governance entity, applies the
// caller's connection boundary, and records what it removed on the entity;
// it returns the visible dataset URNs for the Document.
//
// A failed carrier search does NOT fail the fetch: the vocabulary entry is
// already resolved, and its definition is the answer the caller asked for, so
// losing the membership list costs the extra context rather than the read. The
// truncation is reported (MoreDatasets) rather than left to look like the whole
// set.
func (p *GovernanceProvider) attachCarriers(
	ctx context.Context, kind governanceKind, urn string, caller Caller, entity *GovernanceEntity,
) []string {
	results, total, err := p.searchCarriers(ctx, kind.carriers(urn, carrierLimit))
	if err != nil {
		slog.Debug("governance carrier search skipped", "urn", urn, "error", err)
		return nil
	}
	// An uncounted catalog leaves the membership unverified, which is reported as
	// "there are more" rather than as a complete list: the reader's next move is
	// the same either way (page the catalog by the entity), and the alternative
	// presents a bounded list as the entity's whole membership.
	entity.MoreDatasets = total == semantic.TotalUnknown || total > len(results)
	urns := make([]string, 0, len(results))
	for i := range results {
		if results[i].URN == "" {
			continue
		}
		if !caller.allowsURN(results[i].URN) {
			entity.DatasetsWithheld++
			continue
		}
		entity.Datasets = append(entity.Datasets, GovernanceDataset{
			URN:  results[i].URN,
			Name: results[i].Name,
		})
		urns = append(urns, results[i].URN)
	}
	entity.Notice = withheldContentNotice(entity.DatasetsWithheld, caller.Persona)
	return urns
}

// Verify the provider and its optional fetch capability at compile time.
var (
	_ Provider = (*GovernanceProvider)(nil)
	_ Fetcher  = (*GovernanceProvider)(nil)
)
