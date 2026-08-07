package datahubapi

// Display names for governance URNs (#1159). A knowledge page stores a
// reference to a glossary term, a tag, or a domain as the bare URN, and the key
// inside that URN is not a name: DataHub generates a UUID for an entity created
// without an explicit id, which every create on this surface is. So a chip
// rendered from the URN alone reads as "8f3c1a…" where the page meant "Net
// Revenue".
//
// Resolution lives here rather than in the portal because only this package
// holds the DataHub connections, and it is batch because that is what the
// available upstream reads reward: a term has a by-URN read, but a tag and a
// domain have none at all, so each is resolved by listing its vocabulary once
// and matching — a cost paid per request, not per reference.

import (
	"context"
	"maps"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	// labelResolveTimeout bounds the whole resolve. It is called on the page-read
	// path, where a slow or unreachable DataHub must degrade to the URN-derived
	// label rather than hold the page open.
	labelResolveTimeout = 5 * time.Second

	// termResolveConcurrency caps the simultaneous by-URN term reads, so a page
	// citing many terms does not open one upstream request per term at once.
	termResolveConcurrency = 4

	// vocabularyListLimit is the page asked for when listing a vocabulary to
	// match a URN against. The adapter clamps it; a URN past the clamp stays
	// unresolved and its caller falls back to the URN-derived label.
	vocabularyListLimit = 200
)

// Labeler resolves DataHub governance URNs to their display names over the
// configured connections. A reference carries no connection, so each connection
// is asked in registration order and the first that knows a URN names it.
type Labeler struct {
	bridge Bridge
}

// NewLabeler returns a Labeler over the given bridge.
func NewLabeler(bridge Bridge) *Labeler {
	return &Labeler{bridge: bridge}
}

// Labels returns the display name of each governance URN it could resolve,
// keyed by URN. A URN of any other kind, one no connection knows, and every URN
// when the resolve fails or times out are simply absent: the caller keeps its
// own fallback label rather than being handed a wrong one.
func (l *Labeler) Labels(ctx context.Context, urns []string) map[string]string {
	want := governanceURNs(urns)
	if want.empty() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, labelResolveTimeout)
	defer cancel()

	out := make(map[string]string, want.count())
	for _, conn := range l.bridge.Connections() {
		reader, ok := l.bridge.Reader(conn.Name)
		if !ok {
			continue
		}
		resolveFromReader(ctx, reader, want.unresolved(out), out)
		if len(out) == want.count() {
			break
		}
	}
	return out
}

// wantedURNs is the set of URNs to resolve, split by the read each kind needs:
// a term has a by-URN read, a tag and a domain have only their vocabulary list.
type wantedURNs struct {
	terms   []string
	tags    []string
	domains []string
}

func (w wantedURNs) empty() bool { return w.count() == 0 }

func (w wantedURNs) count() int { return len(w.terms) + len(w.tags) + len(w.domains) }

// unresolved returns the subset not yet named by out, so a second connection is
// only asked about what the first could not answer.
func (w wantedURNs) unresolved(out map[string]string) wantedURNs {
	if len(out) == 0 {
		return w
	}
	keep := func(urns []string) []string {
		left := make([]string, 0, len(urns))
		for _, urn := range urns {
			if _, done := out[urn]; !done {
				left = append(left, urn)
			}
		}
		return left
	}
	return wantedURNs{terms: keep(w.terms), tags: keep(w.tags), domains: keep(w.domains)}
}

// governanceURNs classifies and de-duplicates the URNs worth resolving. A
// dataset URN is not among them: its name is in the URN itself, so resolving it
// would spend an upstream read to learn what the caller already has.
func governanceURNs(urns []string) wantedURNs {
	var want wantedURNs
	seen := make(map[string]bool, len(urns))
	for _, urn := range urns {
		if seen[urn] {
			continue
		}
		seen[urn] = true
		switch datahubEntityType(urn) {
		case "glossaryTerm":
			want.terms = append(want.terms, urn)
		case "tag":
			want.tags = append(want.tags, urn)
		case fieldDomain:
			want.domains = append(want.domains, urn)
		}
	}
	return want
}

// resolveFromReader names what it can from one connection, merging into out.
// The three kinds are independent upstream reads, so they run concurrently; a
// failing one leaves its URNs unresolved rather than failing the others.
func resolveFromReader(ctx context.Context, reader Reader, want wantedURNs, out map[string]string) {
	if want.empty() {
		return
	}
	var mu sync.Mutex
	merge := func(names map[string]string) {
		mu.Lock()
		defer mu.Unlock()
		maps.Copy(out, names)
	}

	// Every task returns nil: one kind that cannot be read must not cancel the
	// reads of the others, so a failure is an absent name rather than an error.
	g := new(errgroup.Group)
	g.Go(func() error {
		merge(resolveTerms(ctx, reader, want.terms))
		return nil
	})
	g.Go(func() error {
		merge(matchVocabulary(want.tags, listTags(ctx, reader, want.tags)))
		return nil
	})
	g.Go(func() error {
		merge(matchVocabulary(want.domains, listDomains(ctx, reader, want.domains)))
		return nil
	})
	_ = g.Wait()
}

// resolveTerms reads each term by URN. The reads are independent, so they run
// concurrently under a bound; a term the connection does not hold is skipped.
func resolveTerms(ctx context.Context, reader Reader, urns []string) map[string]string {
	if len(urns) == 0 {
		return nil
	}
	var mu sync.Mutex
	out := make(map[string]string, len(urns))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(termResolveConcurrency)
	for _, urn := range urns {
		g.Go(func() error {
			term, err := reader.GetGlossaryTerm(gctx, urn)
			if err != nil || term == nil || term.Name == "" {
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			out[urn] = term.Name
			return nil
		})
	}
	// Every task returns nil: one term that cannot be read must not cancel the
	// reads of the others, so a failure is an absent name, not an error here.
	_ = g.Wait()
	return out
}

// listTags lists the connection's tag vocabulary, or nothing when no tag needs
// resolving or the read fails.
func listTags(ctx context.Context, reader Reader, urns []string) []semantic.EntityRef {
	if len(urns) == 0 {
		return nil
	}
	refs, err := reader.SearchTags(ctx, "", vocabularyListLimit)
	if err != nil {
		return nil
	}
	return refs
}

// listDomains lists the connection's domains, or nothing when no domain needs
// resolving or the read fails.
func listDomains(ctx context.Context, reader Reader, urns []string) []semantic.EntityRef {
	if len(urns) == 0 {
		return nil
	}
	refs, err := reader.ListDomains(ctx)
	if err != nil {
		return nil
	}
	return refs
}

// matchVocabulary names the wanted URNs found in a listed vocabulary. An entry
// with no name is not a match: it would replace the URN-derived label with an
// empty chip.
func matchVocabulary(want []string, listed []semantic.EntityRef) map[string]string {
	if len(want) == 0 || len(listed) == 0 {
		return nil
	}
	byURN := make(map[string]string, len(listed))
	for _, ref := range listed {
		if ref.Name != "" {
			byURN[ref.URN] = ref.Name
		}
	}
	out := make(map[string]string, len(want))
	for _, urn := range want {
		if name, ok := byURN[urn]; ok {
			out[urn] = name
		}
	}
	return out
}
