package knowledge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// sourceErrFmt wraps a browse sentinel (ErrUnknownSource / ErrSourceNotBrowsable)
// with the offending source name.
const sourceErrFmt = "source %q: %w"

// Browse limit bounds. A browse page is an enumeration page, not a relevance
// display budget, so it is allowed to be larger than a search's display budget;
// the cap still bounds one round trip's DB/catalog load.
const (
	defaultBrowseLimit = 50
	maxBrowseLimit     = 100
)

// Errors the browse surface distinguishes so the caller can explain a failed
// enumeration precisely instead of returning an opaque empty page.
var (
	// ErrUnknownSource is returned when the requested source name matches no known
	// source (a typo or an unsupported name).
	ErrUnknownSource = errors.New("unknown source")
	// ErrSourceNotBrowsable is returned when the source is known but cannot be
	// enumerated: it is not configured on this deployment, it does not implement
	// browse, or the caller's identity does not grant it.
	ErrSourceNotBrowsable = errors.New("source cannot be enumerated")
)

// BrowseQuery is one enumeration request: page the source from Offset for Limit
// members, scoped to Caller. Unlike a search Query it carries no intent and no
// entity URNs; it enumerates a single source in full rather than ranking across
// many.
type BrowseQuery struct {
	Caller Caller
	Offset int
	Limit  int
}

// BrowsePage is one page of an enumeration: the members on this page (as Hits, so
// each carries the same Reference search emits and fetch consumes), the Total
// members in the source (so a caller knows how many pages remain), and the
// effective Offset/Limit the Router applied after clamping.
type BrowsePage struct {
	Hits   []Hit `json:"hits"`
	Total  int   `json:"total"`
	Offset int   `json:"offset"`
	Limit  int   `json:"limit"`
}

// Browser is the optional capability of a Provider to enumerate its members in
// full, paginated and with a total count and no relevance threshold (#695). It is
// the complement of Search: Search ranks a relevant few, Browse lists the whole
// set so an agent can audit, dedup, govern, or migrate the corpus. A provider that
// does not implement Browser is search-only, and the Router reports its source as
// not browsable.
//
// A per-user provider's Browse must scope to the caller exactly as its Search does;
// the Router additionally refuses to browse a per-user provider for an anonymous
// caller, so enumeration can never widen what a caller could otherwise see.
type Browser interface {
	// Browse returns one page of this provider's members. It sets Hits and Total;
	// the Router stamps the effective Offset and Limit onto the returned page.
	Browse(ctx context.Context, q BrowseQuery) (BrowsePage, error)
}

// clampBrowseLimit constrains a requested browse page size to valid bounds.
func clampBrowseLimit(limit int) int { return clampInt(limit, defaultBrowseLimit, maxBrowseLimit) }

// Browse enumerates a single source in full: the page of members at the requested
// offset plus the source's total member count. It is the exhaustive counterpart to
// Search, for auditing or migrating a corpus that relevance ranking cannot list.
//
// The source must be named exactly (browse enumerates one source; a single offset
// and total are meaningless across a federation). A name that matches no known
// source returns ErrUnknownSource; a known source that is not configured here, does
// not implement Browser, or is a per-user source for an anonymous caller returns
// ErrSourceNotBrowsable. Offset is floored at zero and Limit is clamped to the
// browse bounds; both effective values are echoed on the returned page.
func (r *Router) Browse(ctx context.Context, source string, q BrowseQuery) (BrowsePage, error) {
	// Normalize the source name the same way the search path does (sourceSet /
	// unknownSources lower-case and trim), so browse and search agree on what name
	// is valid: "Knowledge_Pages" resolves identically in both.
	source = strings.ToLower(strings.TrimSpace(source))
	if q.Offset < 0 {
		q.Offset = 0
	}
	q.Limit = clampBrowseLimit(q.Limit)

	for _, p := range r.providers {
		if p.Name() != source {
			continue
		}
		b, ok := p.(Browser)
		if !ok {
			return BrowsePage{}, fmt.Errorf(sourceErrFmt, source, ErrSourceNotBrowsable)
		}
		// Same per-user gate Search and Fetch apply: an anonymous caller is never
		// offered a per-user source, so it can neither search, fetch, nor enumerate it.
		if p.Scope() == ScopePerUser && q.Caller.Anonymous() {
			return BrowsePage{}, fmt.Errorf(sourceErrFmt, source, ErrSourceNotBrowsable)
		}
		page, err := b.Browse(ctx, q)
		if err != nil {
			// The provider already contextualizes its error; pass it through unchanged.
			return BrowsePage{}, err //nolint:wrapcheck // provider error is already contextualized
		}
		page.Offset, page.Limit = q.Offset, q.Limit
		return page, nil
	}

	if knownSourceNames[source] {
		// Known name, but no provider for it on this deployment (e.g. a catalog source
		// without DataHub configured): a configuration gap, not a typo.
		return BrowsePage{}, fmt.Errorf(sourceErrFmt, source, ErrSourceNotBrowsable)
	}
	return BrowsePage{}, fmt.Errorf(sourceErrFmt, source, ErrUnknownSource)
}

// BrowsableSources returns the names of the sources that implement Browser, sorted,
// so a caller (and the tool description) can name exactly what can be enumerated on
// this deployment rather than guessing.
func (r *Router) BrowsableSources() []string {
	var names []string
	for _, p := range r.providers {
		if _, ok := p.(Browser); ok {
			names = append(names, p.Name())
		}
	}
	sort.Strings(names)
	return names
}
