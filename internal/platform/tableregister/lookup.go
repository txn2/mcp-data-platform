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

// TablesFor returns the table reference for every subject that has one.
//
// A subject with several registrations -- the same file registered on two
// connections -- yields the first by the store's ordering, which is the most
// recent. A hit carries one table reference because it is a pointer to where
// the data can be queried, not an inventory of every place it was registered.
func (l *Lookup) TablesFor(
	ctx context.Context, subjects []knowledge.TableSubject,
) (map[string]*knowledge.HitTable, error) {
	if l == nil || l.reg == nil || !l.reg.Available() || len(subjects) == 0 {
		return nil, nil //nolint:nilnil // no tables is an answer, not a failure
	}

	index, byKind := indexSubjects(subjects)
	out := make(map[string]*knowledge.HitTable, len(index))
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
			out[id] = hitTable(regs[0], subject)
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

// hitTable renders one registration as the reference a hit carries.
func hitTable(reg Registration, subject knowledge.TableSubject) *knowledge.HitTable {
	return &knowledge.HitTable{
		Connection: reg.Connection,
		Table:      reg.QualifiedName(),
		Sample:     SampleJoinSQL(reg),
		Stale:      reg.IsStale(subject.Bucket, subject.HeadKey),
	}
}

// Verify the adapter satisfies the discovery-side capability.
var _ knowledge.TableLookup = (*Lookup)(nil)
