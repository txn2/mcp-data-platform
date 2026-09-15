// Package connstored answers what connections a deployment holds, from the
// store that holds them.
//
// A connection exists because there is a row for it. Several replicas run over
// one database, and a connection saved through one of them is a row before it
// is anything in the others' memory, so an inventory taken from what a process
// serves is an inventory of that process: an operator who adds a connection and
// asks what exists is answered differently depending on which replica the load
// balancer picked (#1757).
//
// What a row cannot answer on its own is how large a connection's surface is,
// because that is a property of the specs its catalog holds — also rows, in a
// different table. The count is resolved here rather than by the enumeration,
// so a caller deciding whether to look further at a connection is told the same
// number wherever it asks.
//
// It holds no platform types: the store arrives as a generic lister with a
// projection, which is what lets pkg/platform build one without this package
// importing it back.
package connstored

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/connview"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// Inventory is the platform's connection store, as far as an enumeration reads
// it: every saved connection of every kind, and whether the store outlives the
// process.
type Inventory[R any] interface {
	List(ctx context.Context) ([]R, error)
	Persistent() bool
}

// Row is one stored connection as this package needs it, projected from
// whatever record type the store keeps. CatalogID is the catalog the
// connection names, empty for a connection that names none.
type Row struct {
	Kind        string
	Name        string
	Description string
	CatalogID   string
}

// New adapts the platform's connection store to the inventory an enumeration
// reads, resolving each connection's operation count against catalogs when a
// catalog store is wired.
//
// catalogs is asked for the store rather than handed one, because an
// enumeration built while the platform is still assembling itself would
// otherwise capture the nil the catalog store is before it is wired and report
// no surface for the life of the process.
//
// A nil store, or one that does not outlive the process and so holds nothing
// another replica wrote, adapts to nil: where every replica knows only what it
// was handed, what it was handed is the whole inventory.
func New[R any](store Inventory[R], catalogs func() apicatalog.Store, row func(R) Row) connview.StoreLister {
	if store == nil || !store.Persistent() || row == nil {
		return nil
	}
	return inventory[R]{store: store, catalogs: catalogs, row: row}
}

// inventory is New's adapter.
type inventory[R any] struct {
	store    Inventory[R]
	catalogs func() apicatalog.Store
	row      func(R) Row
}

// ListStoredConnections returns every connection the store holds.
func (i inventory[R]) ListStoredConnections(ctx context.Context) ([]connview.Stored, error) {
	records, err := i.store.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing the connection store: %w", err)
	}
	out := make([]connview.Stored, 0, len(records))
	counts := make(map[string]int)
	catalogs := i.catalogs()
	for _, record := range records {
		r := i.row(record)
		out = append(out, connview.Stored{
			Kind:           r.Kind,
			Name:           r.Name,
			Description:    r.Description,
			CatalogID:      r.CatalogID,
			OperationCount: operations(ctx, catalogs, r.CatalogID, counts),
		})
	}
	return out, nil
}

// operations reports how many operations the named catalog exposes, summed
// over its specs, memoizing per call: two connections on one catalog are the
// point of a catalog, and they must not read it twice.
//
// A catalog that cannot be read reports no count rather than failing the
// enumeration: the connection exists whether or not its surface can be
// measured, and reporting it without a count is what a connection that names
// no catalog already reports.
func operations(ctx context.Context, catalogs apicatalog.Store, catalogID string, counts map[string]int) int {
	if catalogID == "" || catalogs == nil {
		return 0
	}
	if n, ok := counts[catalogID]; ok {
		return n
	}
	specs, err := catalogs.ListSpecs(ctx, catalogID)
	if err != nil {
		slog.WarnContext(ctx, "reading a catalog's specs for the connection inventory failed",
			"catalog_id", logsan.SanitizeForLog(catalogID), "error", logsan.SanitizeForLog(err.Error()))
		counts[catalogID] = 0
		return 0
	}
	total := 0
	for _, spec := range specs {
		total += spec.OperationCount
	}
	counts[catalogID] = total
	return total
}
