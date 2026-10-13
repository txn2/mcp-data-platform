package catalogscan

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// The connection kinds the scan reads.
const (
	KindTrino   = "trino"
	KindAPI     = "api"
	KindGraphQL = "graphql"
)

// systemCatalog is Trino's own metadata catalog. It and every catalog's
// information_schema describe the engine rather than data, and are never
// cataloged.
const systemCatalog = "system"

// ColumnRow is one row of a catalog's information_schema.columns, joined to
// its table's type.
type ColumnRow struct {
	Schema    string
	Table     string
	TableType string
	Column    string
	DataType  string
	Nullable  bool
}

// TrinoSource catalogs every table and view a Trino connection can see.
type TrinoSource struct{ reader TrinoReader }

// NewTrinoSource builds the source.
func NewTrinoSource(r TrinoReader) *TrinoSource { return &TrinoSource{reader: r} }

// Kind is "trino".
func (*TrinoSource) Kind() string { return KindTrino }

// Connections are the live Trino connections.
func (s *TrinoSource) Connections() []Connection { return s.reader.Connections() }

// Types are tables and views; a registered or webhook table keeps its own
// type and is never marked removed by a scan.
func (*TrinoSource) Types() []catalog.DatasetType {
	return []catalog.DatasetType{catalog.DatasetTable, catalog.DatasetView}
}

// Datasets reads each catalog in turn. A catalog that cannot be read is
// reported as unread after the others have been read, so one broken connector
// does not hide the rest, and nothing in it is marked removed.
func (s *TrinoSource) Datasets(ctx context.Context, c Connection, skip Exclusions, yield func(catalog.DatasetInput) error) error {
	catalogs, err := s.reader.Catalogs(ctx, c.Bound)
	if err != nil {
		return fmt.Errorf("listing catalogs: %w", err)
	}
	unread := make([]Unread, 0)
	for _, name := range catalogs {
		if name == systemCatalog || skip.Excludes(name) {
			continue
		}
		if err := s.catalogDatasets(ctx, c, name, skip, yield); err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return fmt.Errorf("scanning stopped: %w", cerr)
			}
			// A dataset the catalog cannot store stops the scan: the
			// failure is the platform's, not the catalog's.
			var w *writeError
			if errors.As(err, &w) {
				return w.err
			}
			unread = append(unread, Unread{
				Path:  catalog.DatasetRef(KindTrino, c.Instance, name).PathString(),
				Error: err.Error(),
			})
		}
	}
	if len(unread) > 0 {
		return &UnreadError{Parts: unread}
	}
	return nil
}

// writeError carries a failure of the scan's own write out of a catalog's
// read, so it is not taken for the catalog being unreadable.
type writeError struct{ err error }

func (w *writeError) Error() string { return w.err.Error() }

func (w *writeError) Unwrap() error { return w.err }

// catalogDatasets groups one catalog's column rows into datasets.
func (s *TrinoSource) catalogDatasets(
	ctx context.Context, c Connection, catalogName string, skip Exclusions, yield func(catalog.DatasetInput) error,
) error {
	var cur *catalog.DatasetInput
	flush := func() error {
		if cur == nil {
			return nil
		}
		in := *cur
		cur = nil
		if err := yield(in); err != nil {
			return &writeError{err: err}
		}
		return nil
	}
	err := s.reader.Columns(ctx, c.Bound, catalogName, func(row ColumnRow) error {
		if skip.Excludes(catalogName, row.Schema, row.Table) {
			return nil
		}
		if cur == nil || cur.Ref.Path[1] != row.Schema || cur.Ref.Path[2] != row.Table {
			if err := flush(); err != nil {
				return err
			}
			cur = &catalog.DatasetInput{
				Ref:  catalog.DatasetRef(KindTrino, c.Instance, catalogName, row.Schema, row.Table),
				Type: tableType(row.TableType),
			}
		}
		cur.Columns = append(cur.Columns, catalog.ColumnInput{Name: row.Column, DataType: row.DataType, Nullable: row.Nullable})
		return nil
	})
	if err != nil {
		return fmt.Errorf("reading columns: %w", err)
	}
	return flush()
}

// tableType maps information_schema.tables.table_type to a dataset type.
func tableType(t string) catalog.DatasetType {
	if t == "VIEW" {
		return catalog.DatasetView
	}
	return catalog.DatasetTable
}

// Operation is one operation an API or GraphQL connection exposes, with the
// inputs it takes as columns.
type Operation struct {
	// Path is the operation's name under its connection: the operation id for
	// an API, the kind and dotted field path for GraphQL (query.repository).
	Path    []string
	Columns []catalog.ColumnInput
}

// OperationSource catalogs the operations of API or GraphQL connections.
type OperationSource struct {
	kind   string
	typ    catalog.DatasetType
	reader OperationReader
}

// NewAPISource catalogs every stored OpenAPI operation of each API connection.
func NewAPISource(r OperationReader) *OperationSource {
	return &OperationSource{kind: KindAPI, typ: catalog.DatasetAPIOperation, reader: r}
}

// NewGraphQLSource catalogs every operation of each GraphQL connection's
// stored schema.
func NewGraphQLSource(r OperationReader) *OperationSource {
	return &OperationSource{kind: KindGraphQL, typ: catalog.DatasetGraphQLOperation, reader: r}
}

// Kind is the connection kind.
func (s *OperationSource) Kind() string { return s.kind }

// Connections are the live connections of the kind.
func (s *OperationSource) Connections() []Connection { return s.reader.Connections() }

// Types is the one operation type the source produces.
func (s *OperationSource) Types() []catalog.DatasetType { return []catalog.DatasetType{s.typ} }

// Datasets yields one dataset per operation.
func (s *OperationSource) Datasets(ctx context.Context, c Connection, skip Exclusions, yield func(catalog.DatasetInput) error) error {
	ops, err := s.reader.Operations(ctx, c.Bound)
	if err != nil {
		return fmt.Errorf("listing operations: %w", err)
	}
	for _, op := range ops {
		if len(op.Path) == 0 || skip.Excludes(op.Path...) {
			continue
		}
		if err := yield(catalog.DatasetInput{
			Ref:     catalog.DatasetRef(s.kind, c.Instance, op.Path...),
			Type:    s.typ,
			Columns: op.Columns,
		}); err != nil {
			return err
		}
	}
	return nil
}
