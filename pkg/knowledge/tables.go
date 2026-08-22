package knowledge

import "context"

// Table subject kinds. They name the two things a file can arrive in the
// platform as, and they are spelled here rather than imported so the discovery
// layer keeps depending on the capability instead of on the registrar that
// implements it -- the same shape as ConnectionScope and ResourceSearcher.
const (
	// TableKindResource is a managed resource.
	TableKindResource = "resource"
	// TableKindAsset is a portal asset.
	TableKindAsset = "asset"
)

// TableSubject identifies one record a table might be registered over.
//
// Bucket and HeadKey come along because staleness is not a property of the
// registration alone: a table is stale when the source's current content moved
// to a directory the table does not point at, which only the record knows.
type TableSubject struct {
	Kind    string
	ID      string
	Bucket  string
	HeadKey string
}

// TableLookup answers which of a set of records are readable as tables.
//
// It is a batch call because the caller is a page of search hits or a list
// view: one query for the page, not one per hit. The result is keyed by
// subject id, and a subject with no registration is simply absent.
type TableLookup interface {
	TablesFor(ctx context.Context, subjects []TableSubject) (map[string]*HitTable, error)
}

// tableLookupSink is the optional capability of a Provider to carry a table
// reference on the hits and documents it serves. The two file-backed sources
// (resources, assets) implement it; every other provider is left alone.
type tableLookupSink interface {
	SetTableLookup(TableLookup)
}

// SetTableLookup binds the lookup that tells a file-backed hit whether it is
// readable as a query-engine table (#1327), pushing it into every provider
// that can carry one. Nil (the default) leaves hits as they were, which is
// what a deployment with no registration mechanism serves. Not safe for
// concurrent use with Search; call once at wiring time.
func (r *Router) SetTableLookup(lookup TableLookup) {
	for _, p := range r.providers {
		if sink, ok := p.(tableLookupSink); ok {
			sink.SetTableLookup(lookup)
		}
	}
}

// subjectsOf collects the records behind a page of hits, skipping any hit that
// has none.
func subjectsOf(hits []Hit, subjectFor func(Hit) (TableSubject, bool)) []TableSubject {
	subjects := make([]TableSubject, 0, len(hits))
	for _, h := range hits {
		if s, ok := subjectFor(h); ok {
			subjects = append(subjects, s)
		}
	}
	return subjects
}

// lookupOneTable is the fetch-side single-subject read. Like attachTables it
// swallows failure: a fetched record must not disappear because the
// registration lookup did.
func lookupOneTable(ctx context.Context, lookup TableLookup, subject TableSubject) *HitTable {
	if lookup == nil || subject.ID == "" {
		return nil
	}
	tables, err := lookup.TablesFor(ctx, []TableSubject{subject})
	if err != nil {
		return nil
	}
	return tables[subject.ID]
}

// attachTables fills in the Table field of every hit whose record carries a
// registration.
//
// Failure is not an error the caller sees: a table reference is an addition to
// a hit, and losing it must not lose the hit. subjectFor maps a hit back to the
// record it came from, which only the provider knows.
func attachTables(
	ctx context.Context,
	lookup TableLookup,
	hits []Hit,
	subjectFor func(Hit) (TableSubject, bool),
) {
	if lookup == nil || len(hits) == 0 {
		return
	}
	subjects := subjectsOf(hits, subjectFor)
	if len(subjects) == 0 {
		return
	}
	tables, err := lookup.TablesFor(ctx, subjects)
	if err != nil || len(tables) == 0 {
		return
	}
	for i := range hits {
		s, ok := subjectFor(hits[i])
		if !ok {
			continue
		}
		if t, found := tables[s.ID]; found {
			hits[i].Table = t
		}
	}
}
