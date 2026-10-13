package tableregister

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// CatalogWriter is what cataloging a registered table needs from the platform
// catalog (#1918, #1922).
type CatalogWriter interface {
	PutDataset(ctx context.Context, in catalog.DatasetInput) (*catalog.Dataset, error)
	MarkDatasetRemoved(ctx context.Context, ref catalog.Ref) error
	AddEdges(ctx context.Context, edges []catalog.Edge) error
}

// catalogKind is the connection kind every registered table is on.
const catalogKind = "trino"

// CatalogingStore is a registration Store that also writes each table it
// records to the platform catalog, with a lineage edge from the file the table
// reads. A registration made through manage_table and a webhook source's table
// both pass through Insert, so both are cataloged by the time the call that
// made them returns rather than on the next scan.
//
// A catalog write that fails is logged and does not fail the registration: the
// table exists and is recorded, and the next scan catalogs it.
type CatalogingStore struct {
	Store
	catalog CatalogWriter
	// instance returns the instance name of the connection a registration
	// names by the name a call binds it by.
	instance func(bound string) string
}

// NewCatalogingStore wraps inner. instance maps a registration's connection to
// the instance name catalog references carry; nil uses it as it stands.
func NewCatalogingStore(inner Store, cat CatalogWriter, instance func(bound string) string) *CatalogingStore {
	if instance == nil {
		instance = func(bound string) string { return bound }
	}
	return &CatalogingStore{Store: inner, catalog: cat, instance: instance}
}

// Insert records the registration, then catalogs its table.
func (s *CatalogingStore) Insert(ctx context.Context, r Registration) error {
	if err := s.Store.Insert(ctx, r); err != nil {
		return err //nolint:wrapcheck // the decorated store's error, unchanged
	}
	s.catalogTable(ctx, r)
	return nil
}

// Relocate moves the registration, then records the columns the table now
// declares.
func (s *CatalogingStore) Relocate(ctx context.Context, id, location, format string, columns []Column) error {
	if err := s.Store.Relocate(ctx, id, location, format, columns); err != nil {
		return err //nolint:wrapcheck // the decorated store's error, unchanged
	}
	r, err := s.Get(ctx, id)
	switch {
	case err != nil:
		slog.WarnContext(ctx, "catalog: reading a relocated registration failed", "registration", logsan.SanitizeForLog(id),
			logFieldError, logsan.SanitizeForLog(err.Error()))
	case r != nil:
		s.catalogTable(ctx, *r)
	}
	return nil
}

// Delete removes the registration, then marks its table removed in the
// catalog: a scan does not mark a registered table removed, so without this an
// unregistered table would stay listed. Its curation stays readable.
func (s *CatalogingStore) Delete(ctx context.Context, id string) error {
	r, getErr := s.Get(ctx, id)
	if err := s.Store.Delete(ctx, id); err != nil {
		return err //nolint:wrapcheck // the decorated store's error, unchanged
	}
	switch {
	case getErr != nil:
		slog.WarnContext(ctx, "catalog: reading an unregistered table failed", "registration", logsan.SanitizeForLog(id),
			logFieldError, logsan.SanitizeForLog(getErr.Error()))
	case r != nil:
		if err := s.catalog.MarkDatasetRemoved(ctx, s.datasetRef(*r)); err != nil {
			slog.WarnContext(ctx, "catalog: marking an unregistered table removed failed", logFieldTable, logsan.SanitizeForLog(r.QualifiedName()),
				logFieldError, logsan.SanitizeForLog(err.Error()))
		}
	}
	return nil
}

// datasetRef is the catalog reference of a registration's table.
func (s *CatalogingStore) datasetRef(r Registration) catalog.Ref {
	return catalog.DatasetRef(catalogKind, s.instance(r.Connection), r.Catalog, r.Schema, r.Table)
}

// catalogTable writes the registration's table and its lineage edge.
func (s *CatalogingStore) catalogTable(ctx context.Context, r Registration) {
	ref := s.datasetRef(r)
	in := catalog.DatasetInput{Ref: ref, Type: catalog.DatasetRegisteredTable}
	if r.SourceKind == KindWebhook {
		in.Type = catalog.DatasetWebhookTable
	}
	if r.SourceKind == KindResource {
		in.SchemaResourceID = r.SourceID
	}
	for _, c := range r.Columns {
		in.Columns = append(in.Columns, catalog.ColumnInput{Name: c.Name, DataType: c.Type, Nullable: true})
	}
	if _, err := s.catalog.PutDataset(ctx, in); err != nil {
		slog.WarnContext(ctx, "catalog: writing a registered table failed", logFieldTable, logsan.SanitizeForLog(r.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(err.Error()))
		return
	}
	source := sourceRef(r)
	if source == "" {
		return
	}
	if err := s.catalog.AddEdges(ctx, []catalog.Edge{{
		Source: source, Target: ref.String(), WriterKind: catalog.WriterRegistration,
		WriterRef: r.ID, WrittenBy: r.RegisteredBy,
	}}); err != nil {
		slog.WarnContext(ctx, "catalog: recording a registration's lineage failed", logFieldTable, logsan.SanitizeForLog(r.QualifiedName()),
			logFieldError, logsan.SanitizeForLog(err.Error()))
	}
}

// sourceRef is the reference of the file a registration reads. A webhook
// source's table reads many compacted windows rather than one file, so its
// registration names none.
func sourceRef(r Registration) string {
	switch r.SourceKind {
	case KindResource:
		return "mcp:resource:" + r.SourceID
	case KindAsset:
		return "mcp:asset:" + r.SourceID
	default:
		return ""
	}
}
