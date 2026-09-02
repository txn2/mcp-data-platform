package datahub

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// The parts of a dataset record read beside the entity itself, named in
// Dataset.Unavailable when the catalog cannot serve one.
const (
	datasetPartSchema    = "schema"
	datasetPartQueries   = "queries"
	datasetPartDocuments = "related_documents"
)

// GetDataset reads the full record of one dataset (#1590): the entity (with
// the same 1.4.x extras GetTableContext folds in), its declared schema, its
// saved queries, and its linked context documents, issued concurrently since
// the four reads are independent.
//
// The entity read is the record: its failure is the dataset's, and DataHub
// reports a missing dataset as an error, so the caller's not-found rule
// applies to it. The other three are parts of a record that exists; a part the
// catalog cannot serve (an older DataHub with no query listing, a transient
// failure on one query) is named in Unavailable rather than dropped silently
// or allowed to fail the whole read.
func (a *Adapter) GetDataset(ctx context.Context, table semantic.TableIdentifier) (*semantic.Dataset, error) {
	urn := a.buildDatasetURN(table)

	var (
		wg      sync.WaitGroup
		schema  *types.SchemaMetadata
		queries *types.QueryList
		docs    []types.Document
		errs    = make(map[string]error, 3)
		mu      sync.Mutex
	)
	part := func(name string, read func() error) {
		wg.Go(func() {
			if err := read(); err != nil {
				mu.Lock()
				errs[name] = fmt.Errorf("getting dataset %s from datahub: %w", name, err)
				mu.Unlock()
			}
		})
	}
	part(datasetPartSchema, func() (err error) { schema, err = a.client.GetSchema(ctx, urn); return err })            //nolint:wrapcheck // wrapped by part
	part(datasetPartQueries, func() (err error) { queries, err = a.client.GetQueries(ctx, urn); return err })         //nolint:wrapcheck // wrapped by part
	part(datasetPartDocuments, func() (err error) { docs, err = a.client.GetRelatedDocuments(ctx, urn); return err }) //nolint:wrapcheck // wrapped by part

	entity, err := a.client.GetEntity(ctx, urn)
	wg.Wait()
	if err != nil {
		return nil, fmt.Errorf("getting entity from datahub: %w", err)
	}

	ds := &semantic.Dataset{
		TableContext:     *a.entityToTableContext(entity),
		Name:             entity.Name,
		Type:             entity.Type,
		Platform:         entity.Platform,
		SubTypes:         entity.SubTypes,
		Created:          convertTimestamp(entity.Created),
		Schema:           convertSchema(schema),
		RelatedDocuments: documentResults(docs),
	}
	if queries != nil {
		ds.Queries = convertQueries(queries.Queries)
		ds.TotalQueries = queries.Total
	}
	for _, name := range []string{datasetPartSchema, datasetPartQueries, datasetPartDocuments} {
		if partErr, failed := errs[name]; failed {
			slog.Debug("dataset part unavailable", "urn", urn, "part", name, "error", partErr)
			ds.Unavailable = append(ds.Unavailable, name)
		}
	}
	return a.sanitizer.SanitizeDataset(ds), nil
}

// GetDataProduct reads one data product by URN (#1590). DataHub reports a
// missing product as an error, so the caller's not-found rule applies to it.
func (a *Adapter) GetDataProduct(ctx context.Context, urn string) (*semantic.DataProduct, error) {
	product, err := a.client.GetDataProduct(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting data product from datahub: %w", err)
	}
	if product == nil {
		return nil, fmt.Errorf("data product not found: %s", urn)
	}
	out := &semantic.DataProduct{
		URN:              product.URN,
		Name:             product.Name,
		Description:      product.Description,
		Domain:           convertDomain(product.Domain),
		Owners:           convertOwners(product.Owners),
		CustomProperties: product.Properties,
	}
	for i := range product.Assets {
		asset := &product.Assets[i]
		out.Assets = append(out.Assets, semantic.EntityRef{
			URN:         asset.URN,
			Name:        asset.Name,
			Description: asset.Description,
		})
	}
	return a.sanitizer.SanitizeDataProduct(out), nil
}

// convertSchema maps a DataHub schema to the declared schema of a dataset
// record; a nil schema (the catalog holds none) stays nil.
func convertSchema(schema *types.SchemaMetadata) *semantic.DatasetSchema {
	if schema == nil {
		return nil
	}
	out := &semantic.DatasetSchema{
		Version:     schema.Version,
		Fields:      make([]semantic.SchemaField, 0, len(schema.Fields)),
		PrimaryKeys: schema.PrimaryKeys,
	}
	for _, f := range schema.Fields {
		out.Fields = append(out.Fields, semantic.SchemaField{
			FieldPath:      f.FieldPath,
			Type:           f.Type,
			NativeType:     f.NativeType,
			Description:    f.Description,
			Nullable:       f.Nullable,
			IsPartitionKey: f.IsPartitionKey,
			Tags:           convertTags(f.Tags),
			GlossaryTerms:  convertGlossaryTerms(f.GlossaryTerms),
		})
	}
	for _, fk := range schema.ForeignKeys {
		out.ForeignKeys = append(out.ForeignKeys, semantic.ForeignKey{
			Name:           fk.Name,
			SourceFields:   fk.SourceFields,
			ForeignDataset: fk.ForeignDataset,
			ForeignFields:  fk.ForeignFields,
		})
	}
	return out
}

// convertQueries maps the catalog's saved queries onto the dataset record.
func convertQueries(queries []types.Query) []semantic.SavedQuery {
	if len(queries) == 0 {
		return nil
	}
	out := make([]semantic.SavedQuery, 0, len(queries))
	for _, q := range queries {
		out = append(out, semantic.SavedQuery{
			URN:         q.URN,
			Name:        q.Name,
			Statement:   q.Statement,
			Description: q.Description,
			Source:      q.Source,
			CreatedBy:   q.CreatedBy,
			Created:     convertTimestamp(q.Created),
		})
	}
	return out
}

// documentResults maps linked context documents onto the dataset record.
func documentResults(docs []types.Document) []semantic.DocumentResult {
	if len(docs) == 0 {
		return nil
	}
	out := make([]semantic.DocumentResult, 0, len(docs))
	for i := range docs {
		out = append(out, toDocumentResult(&docs[i]))
	}
	return out
}

// Verify the adapter serves both fetch arms at compile time.
var (
	_ semantic.DatasetReader     = (*Adapter)(nil)
	_ semantic.DataProductReader = (*Adapter)(nil)
)
