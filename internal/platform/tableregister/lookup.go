package tableregister

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
)

// Lookup adapts a Registrar to the discovery layer's TableLookup: it answers,
// for a page of search hits or one fetched record, which of them are readable
// as query-engine tables.
//
// It lives here rather than in pkg/knowledge because staleness needs both
// halves -- the registration's recorded location and the record's current head
// key -- and only this side knows what a registration is.
type Lookup struct {
	reg *Registrar
}

// NewLookup adapts a Registrar for discovery. A nil or unwired Registrar
// yields a lookup that finds nothing, which is a deployment with no
// registration mechanism rather than an error every search has to carry.
func NewLookup(reg *Registrar) *Lookup { return &Lookup{reg: reg} }

// TablesFor returns every table reference of every subject that has one.
//
// A subject with several registrations yields all of them in the store's
// order, newest first, which is the order manage_table action=list shows.
// Which of them a surface reports is the discovery layer's business, not this
// adapter's: fetch renders them all, a search hit picks one (#1627).
func (l *Lookup) TablesFor(
	ctx context.Context, subjects []knowledge.TableSubject,
) (map[string][]knowledge.HitTable, error) {
	if l == nil || l.reg == nil || !l.reg.Available() || len(subjects) == 0 {
		return nil, nil //nolint:nilnil // no tables is an answer, not a failure
	}

	index, byKind := indexSubjects(subjects)
	out := make(map[string][]knowledge.HitTable, len(index))
	for kind, ids := range byKind {
		found, err := l.reg.ForSources(ctx, kind, ids)
		if err != nil {
			return nil, err
		}
		for id, regs := range found {
			if len(regs) == 0 {
				continue
			}
			subject := index[id]
			tables := make([]knowledge.HitTable, 0, len(regs))
			for _, reg := range regs {
				tables = append(tables, hitTable(reg, subject))
			}
			out[id] = tables
		}
	}
	return out, nil
}

// indexSubjects de-duplicates the page's subjects and groups their ids by
// kind, so each kind costs one query however many hits name it.
func indexSubjects(
	subjects []knowledge.TableSubject,
) (index map[string]knowledge.TableSubject, byKind map[string][]string) {
	index = make(map[string]knowledge.TableSubject, len(subjects))
	byKind = make(map[string][]string, 2)
	for _, s := range subjects {
		if s.ID == "" {
			continue
		}
		if _, seen := index[s.ID]; seen {
			continue
		}
		index[s.ID] = s
		byKind[s.Kind] = append(byKind[s.Kind], s.ID)
	}
	return index, byKind
}

// hitTable renders one registration as the reference a hit or a document
// carries, with the four state fields manage_table action=list reports so the
// surfaces cannot disagree (#1627).
func hitTable(reg Registration, subject knowledge.TableSubject) knowledge.HitTable {
	return knowledge.HitTable{
		RegistrationID: reg.ID,
		Connection:     reg.Connection,
		Table:          reg.QualifiedName(),
		Sample:         SampleJoinSQL(reg),
		Stale:          reg.IsStale(subject.Bucket, subject.HeadKey),
		Follow:         reg.Follow,
		Repair:         reg.Repair,
		FollowError:    reg.FollowError,
	}
}

// Verify the adapter satisfies the discovery-side capability.
var _ knowledge.TableLookup = (*Lookup)(nil)
