// Package catalogstore stores the platform catalog in PostgreSQL (#1917).
package catalogstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Compile-time interface verification.
var _ catalog.Store = (*Store)(nil)

// Store implements catalog.Store over the catalog_entities, catalog_aspects
// and catalog_aspect_history tables.
type Store struct {
	db *sql.DB
}

// New returns a catalog store over db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// StoreFor returns the catalog store over db, or nil on a deployment with no
// database, so a caller can leave the catalog out without a nil check.
func StoreFor(db *sql.DB) catalog.Store {
	if db == nil {
		return nil
	}
	return New(db)
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// entity is the identity of a stored entity, as the write paths need it.
type entity struct {
	id        string
	kind      catalog.Kind
	reference string
}

const upsertDataset = `
INSERT INTO catalog_entities (kind, reference, connection_kind, connection_name, path, name, dataset_type, schema_resource_id, schema_document)
VALUES ('dataset', $1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (reference) DO UPDATE SET
    name = EXCLUDED.name,
    -- A table the platform registered stays one when a scan of the engine
    -- later sees it as a plain table.
    dataset_type = CASE
        WHEN catalog_entities.dataset_type IN ('registered_table', 'webhook_table')
             AND EXCLUDED.dataset_type IN ('table', 'view')
        THEN catalog_entities.dataset_type
        ELSE EXCLUDED.dataset_type END,
    schema_resource_id = CASE WHEN EXCLUDED.schema_resource_id = ''
        THEN catalog_entities.schema_resource_id ELSE EXCLUDED.schema_resource_id END,
    schema_document = CASE WHEN EXCLUDED.schema_document = ''
        THEN catalog_entities.schema_document ELSE EXCLUDED.schema_document END,
    removed_at = NULL,
    updated_at = NOW()
RETURNING id`

const upsertColumn = `
INSERT INTO catalog_entities (kind, reference, connection_kind, connection_name, path, column_name, parent_id, name, data_type, ordinal, nullable)
VALUES ('column', $1, $2, $3, $4, $5, $6, $5, $7, $8, $9)
ON CONFLICT (reference) DO UPDATE SET
    data_type = EXCLUDED.data_type,
    ordinal = EXCLUDED.ordinal,
    nullable = EXCLUDED.nullable,
    updated_at = NOW()`

const deleteUndeclaredColumns = `
DELETE FROM catalog_entities
WHERE kind = 'column' AND parent_id = $1 AND NOT (reference = ANY($2))`

// PutDataset creates or updates a dataset and replaces its column set.
func (s *Store) PutDataset(ctx context.Context, in catalog.DatasetInput) (*catalog.Dataset, error) {
	if err := checkDatasetInput(in); err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = in.Ref.Path[len(in.Ref.Path)-1]
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, upsertDataset,
			in.Ref.String(), in.Ref.ConnectionKind, in.Ref.Connection, in.Ref.PathString(),
			name, string(in.Type), in.SchemaResourceID, in.SchemaDocument,
		).Scan(&id); err != nil {
			return fmt.Errorf("upserting dataset: %w", err)
		}
		refs := make([]string, 0, len(in.Columns))
		for i, c := range in.Columns {
			ref := catalog.ColumnRef(in.Ref, c.Name).String()
			refs = append(refs, ref)
			if _, err := tx.ExecContext(ctx, upsertColumn,
				ref, in.Ref.ConnectionKind, in.Ref.Connection, in.Ref.PathString(),
				c.Name, id, c.DataType, i, c.Nullable,
			); err != nil {
				return fmt.Errorf("upserting column %s: %w", c.Name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, deleteUndeclaredColumns, id, pq.Array(refs)); err != nil {
			return fmt.Errorf("removing undeclared columns: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetDataset(ctx, in.Ref)
}

// checkDatasetInput refuses a declaration the store could not key or read back.
func checkDatasetInput(in catalog.DatasetInput) error {
	if in.Ref.Kind != catalog.KindDataset {
		return fmt.Errorf("want a dataset reference, got %s: %w", in.Ref.Kind, catalog.ErrInvalidRef)
	}
	if err := in.Ref.Validate(); err != nil {
		return fmt.Errorf("dataset reference: %w", err)
	}
	if !in.Type.Valid() {
		return fmt.Errorf("unknown dataset type %q", in.Type)
	}
	seen := make(map[string]bool, len(in.Columns))
	for _, c := range in.Columns {
		if c.Name == "" {
			return errors.New("a column needs a name")
		}
		if seen[c.Name] {
			return fmt.Errorf("column %q declared twice", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

const selectDataset = `
SELECT id, name, dataset_type, schema_resource_id, schema_document, removed_at, created_at, updated_at
FROM catalog_entities WHERE kind = 'dataset' AND reference = $1`

const selectColumns = `
SELECT id, name, data_type, ordinal, nullable
FROM catalog_entities WHERE kind = 'column' AND parent_id = $1
ORDER BY ordinal`

// selectAspectsUnder reads the aspects of an entity and of its children, which
// for a dataset is its columns.
const selectAspectsUnder = `
SELECT a.entity_id, a.aspect, a.value
FROM catalog_aspects a JOIN catalog_entities e ON e.id = a.entity_id
WHERE e.id = $1 OR e.parent_id = $1`

// GetDataset reads a dataset with its columns and curation.
func (s *Store) GetDataset(ctx context.Context, ref catalog.Ref) (*catalog.Dataset, error) {
	if ref.Kind != catalog.KindDataset {
		return nil, fmt.Errorf("want a dataset reference, got %s: %w", ref.Kind, catalog.ErrInvalidRef)
	}
	d := &catalog.Dataset{Reference: ref.String(), Ref: ref}
	var typ, document string
	var removed sql.NullTime
	err := s.db.QueryRowContext(ctx, selectDataset, d.Reference).Scan(
		&d.ID, &d.Name, &typ, &d.SchemaResourceID, &document, &removed, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading dataset: %w", err)
	}
	d.Type = catalog.DatasetType(typ)
	if document != "" {
		d.SchemaDocument = catalog.SchemaDocumentRef(ref.ConnectionKind, ref.Connection, document).String()
	}
	if removed.Valid {
		d.RemovedAt = &removed.Time
	}

	colIDs, err := s.readColumns(ctx, d)
	if err != nil {
		return nil, err
	}
	aspects, err := s.readAspects(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	if a, ok := aspects[d.ID]; ok {
		d.Aspects = *a
	}
	for i, id := range colIDs {
		if a, ok := aspects[id]; ok {
			d.Columns[i].Aspects = *a
		}
	}
	return d, nil
}

// readColumns fills d.Columns in order and returns their entity ids in the
// same order.
func (s *Store) readColumns(ctx context.Context, d *catalog.Dataset) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, selectColumns, d.ID)
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	d.Columns = make([]catalog.Column, 0)
	var ids []string
	for rows.Next() {
		var id string
		var c catalog.Column
		if err := rows.Scan(&id, &c.Name, &c.DataType, &c.Ordinal, &c.Nullable); err != nil {
			return nil, fmt.Errorf("scanning column: %w", err)
		}
		ids = append(ids, id)
		d.Columns = append(d.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}
	return ids, nil
}

// readAspects returns the aspects of an entity and its children, by entity id.
func (s *Store) readAspects(ctx context.Context, id string) (map[string]*catalog.Aspects, error) {
	rows, err := s.db.QueryContext(ctx, selectAspectsUnder, id)
	if err != nil {
		return nil, fmt.Errorf("reading aspects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]*catalog.Aspects)
	for rows.Next() {
		var entityID, name string
		var value []byte
		if err := rows.Scan(&entityID, &name, &value); err != nil {
			return nil, fmt.Errorf("scanning aspect: %w", err)
		}
		a, ok := out[entityID]
		if !ok {
			a = &catalog.Aspects{}
			out[entityID] = a
		}
		if err := a.Set(catalog.AspectName(name), value); err != nil {
			return nil, fmt.Errorf("stored aspect %s on %s: %w", name, entityID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading aspects: %w", err)
	}
	return out, nil
}

const upsertDefinition = `
INSERT INTO catalog_entities (id, kind, reference, slug, name, parent_id)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (kind, slug) WHERE slug <> '' DO UPDATE SET
    name = EXCLUDED.name,
    parent_id = EXCLUDED.parent_id,
    updated_at = NOW()
RETURNING reference`

// PutDefinition creates or updates a glossary term, glossary node, domain,
// data product or tag, keyed by kind and slug.
func (s *Store) PutDefinition(ctx context.Context, in catalog.DefinitionInput) (*catalog.Definition, error) {
	if !isDefinitionKind(in.Kind) {
		return nil, fmt.Errorf("kind %s is not a definition kind: %w", in.Kind, catalog.ErrInvalidRef)
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("a definition needs a name")
	}
	slug := in.Slug
	switch {
	case in.Kind == catalog.KindTag:
		slug = in.Name
	case slug == "":
		slug = catalog.Slug(in.Name)
	}
	if slug == "" {
		return nil, fmt.Errorf("no slug can be derived from %q", in.Name)
	}
	parentID, err := s.definitionParent(ctx, in)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	reference := catalog.KeyRef(in.Kind, id).String()
	if in.Kind == catalog.KindTag {
		reference = catalog.KeyRef(catalog.KindTag, in.Name).String()
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, upsertDefinition,
		id, string(in.Kind), reference, slug, in.Name, parentID,
	).Scan(&stored); err != nil {
		return nil, fmt.Errorf("upserting %s: %w", in.Kind, err)
	}
	return s.GetDefinition(ctx, catalog.KeyRef(in.Kind, slug))
}

// definitionParent resolves the glossary node a term or node is placed under,
// or no parent when none is named. Only those two kinds sit in the glossary
// tree.
func (s *Store) definitionParent(ctx context.Context, in catalog.DefinitionInput) (sql.NullString, error) {
	if in.Parent == "" {
		return sql.NullString{}, nil
	}
	if in.Kind != catalog.KindGlossaryTerm && in.Kind != catalog.KindGlossaryNode {
		return sql.NullString{}, fmt.Errorf("a %s has no parent", in.Kind)
	}
	ref, err := catalog.ParseRef(in.Parent)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("parent: %w", err)
	}
	if ref.Kind != catalog.KindGlossaryNode {
		return sql.NullString{}, fmt.Errorf("parent %q is not a glossary node", in.Parent)
	}
	e, err := resolve(ctx, s.db, ref)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("parent %s: %w", in.Parent, err)
	}
	return sql.NullString{String: e.id, Valid: true}, nil
}

// isDefinitionKind reports whether kind is keyed by id or slug rather than by
// connection and path.
func isDefinitionKind(kind catalog.Kind) bool {
	switch kind {
	case catalog.KindGlossaryTerm, catalog.KindGlossaryNode, catalog.KindTag,
		catalog.KindDomain, catalog.KindDataProduct:
		return true
	default:
		return false
	}
}

const selectDefinition = `
SELECT e.id, e.reference, e.slug, e.name, COALESCE(p.reference, ''), e.created_at, e.updated_at
FROM catalog_entities e LEFT JOIN catalog_entities p ON p.id = e.parent_id
WHERE e.kind = $1 AND (e.reference = $2 OR e.slug = $3)`

// GetDefinition reads a definition by a reference whose key is its id or slug.
func (s *Store) GetDefinition(ctx context.Context, ref catalog.Ref) (*catalog.Definition, error) {
	if !isDefinitionKind(ref.Kind) {
		return nil, fmt.Errorf("kind %s is not a definition kind: %w", ref.Kind, catalog.ErrInvalidRef)
	}
	d := &catalog.Definition{Kind: ref.Kind}
	err := s.db.QueryRowContext(ctx, selectDefinition, string(ref.Kind), ref.String(), ref.Key).Scan(
		&d.ID, &d.Reference, &d.Slug, &d.Name, &d.Parent, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", ref.Kind, err)
	}
	aspects, err := s.readAspects(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	if a, ok := aspects[d.ID]; ok {
		d.Aspects = *a
	}
	return d, nil
}

const resolveByReference = `
SELECT id, kind, reference FROM catalog_entities WHERE reference = $1`

const resolveDefinition = `
SELECT id, kind, reference FROM catalog_entities
WHERE kind = $1 AND (reference = $2 OR slug = $3)`

// resolve finds the stored entity a reference names.
func resolve(ctx context.Context, q querier, ref catalog.Ref) (entity, error) {
	var row *sql.Row
	if isDefinitionKind(ref.Kind) {
		row = q.QueryRowContext(ctx, resolveDefinition, string(ref.Kind), ref.String(), ref.Key)
	} else {
		row = q.QueryRowContext(ctx, resolveByReference, ref.String())
	}
	var e entity
	var kind string
	err := row.Scan(&e.id, &kind, &e.reference)
	if errors.Is(err, sql.ErrNoRows) {
		return entity{}, catalog.ErrNotFound
	}
	if err != nil {
		return entity{}, fmt.Errorf("resolving %s: %w", ref, err)
	}
	e.kind = catalog.Kind(kind)
	return e, nil
}

// WriteAspect writes one aspect and records the change.
func (s *Store) WriteAspect(
	ctx context.Context, ref catalog.Ref, name catalog.AspectName, value json.RawMessage, by string,
) (*catalog.Change, error) {
	var out *catalog.Change
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		e, err := resolve(ctx, tx, ref)
		if err != nil {
			return err
		}
		normalized, err := catalog.NormalizeAspect(e.kind, name, value)
		if err != nil {
			return err //nolint:wrapcheck // the catalog sentinel is the caller's to match
		}
		out, err = writeAspect(ctx, tx, aspectWrite{entity: e, name: name, value: normalized, by: by})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

const lockEntity = `SELECT id FROM catalog_entities WHERE id = $1 FOR UPDATE`

const selectCurrentAspect = `
SELECT value FROM catalog_aspects WHERE entity_id = $1 AND aspect = $2`

const selectLastVersion = `
SELECT COALESCE(MAX(version), 0) FROM catalog_aspect_history WHERE entity_id = $1 AND aspect = $2`

const upsertAspect = `
INSERT INTO catalog_aspects (entity_id, aspect, value, version, written_by, written_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (entity_id, aspect) DO UPDATE SET
    value = EXCLUDED.value, version = EXCLUDED.version,
    written_by = EXCLUDED.written_by, written_at = EXCLUDED.written_at`

const deleteAspect = `DELETE FROM catalog_aspects WHERE entity_id = $1 AND aspect = $2`

const insertHistory = `
INSERT INTO catalog_aspect_history (entity_id, aspect, version, previous, value, written_by, reverts)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, written_at`

// aspectWrite is one write of one aspect: the entity, the aspect, its new
// value (nil clears it), who wrote it, and the change it reverts, if any.
type aspectWrite struct {
	entity  entity
	name    catalog.AspectName
	value   json.RawMessage
	by      string
	reverts string
	// expect, when checked, is the value the writer read; a stored value that
	// differs refuses the write with catalog.ErrStale.
	expect  json.RawMessage
	checked bool
}

// writeAspect replaces one aspect's value inside tx and records the change.
// The entity row is locked first, so two writers of the same entity take
// versions one after the other.
func writeAspect(ctx context.Context, tx *sql.Tx, w aspectWrite) (*catalog.Change, error) {
	e, name, value, by, reverts := w.entity, w.name, w.value, w.by, w.reverts
	if _, err := tx.ExecContext(ctx, lockEntity, e.id); err != nil {
		return nil, fmt.Errorf("locking entity: %w", err)
	}
	var previous []byte
	err := tx.QueryRowContext(ctx, selectCurrentAspect, e.id, string(name)).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("reading current %s: %w", name, err)
	}
	if w.checked && !sameJSON(previous, w.expect) {
		return nil, fmt.Errorf("writing %s of %s: %w", name, e.reference, catalog.ErrStale)
	}
	var last int
	if err := tx.QueryRowContext(ctx, selectLastVersion, e.id, string(name)).Scan(&last); err != nil {
		return nil, fmt.Errorf("reading %s version: %w", name, err)
	}
	version := last + 1
	if value == nil {
		_, err = tx.ExecContext(ctx, deleteAspect, e.id, string(name))
	} else {
		_, err = tx.ExecContext(ctx, upsertAspect, e.id, string(name), []byte(value), version, by)
	}
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", name, err)
	}
	c := &catalog.Change{
		Reference: e.reference, Aspect: name, Version: version,
		Previous: previous, Value: value, WrittenBy: by, Reverts: reverts,
	}
	if err := tx.QueryRowContext(ctx, insertHistory,
		e.id, string(name), version, nullJSON(previous), nullJSON(value), by, nullString(reverts),
	).Scan(&c.ID, &c.WrittenAt); err != nil {
		return nil, fmt.Errorf("recording %s change: %w", name, err)
	}
	return c, nil
}

const selectHistory = `
SELECT h.id, h.version, h.previous, h.value, h.written_by, h.written_at, COALESCE(h.reverts::text, '')
FROM catalog_aspect_history h
WHERE h.entity_id = $1 AND h.aspect = $2
ORDER BY h.version DESC`

// History lists the changes to one aspect, newest first.
func (s *Store) History(ctx context.Context, ref catalog.Ref, name catalog.AspectName) ([]catalog.Change, error) {
	e, err := resolve(ctx, s.db, ref)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, selectHistory, e.id, string(name))
	if err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.Change, 0)
	for rows.Next() {
		c := catalog.Change{Reference: e.reference, Aspect: name}
		var previous, value []byte
		if err := rows.Scan(&c.ID, &c.Version, &previous, &value, &c.WrittenBy, &c.WrittenAt, &c.Reverts); err != nil {
			return nil, fmt.Errorf("scanning history: %w", err)
		}
		c.Previous, c.Value = previous, value
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}
	return out, nil
}

const selectChange = `
SELECT h.aspect, h.previous, e.id, e.kind, e.reference
FROM catalog_aspect_history h JOIN catalog_entities e ON e.id = h.entity_id
WHERE h.id = $1`

// Rollback restores the value a change replaced, recorded as a new change.
func (s *Store) Rollback(ctx context.Context, changeID, by string) (*catalog.Change, error) {
	if _, err := uuid.Parse(changeID); err != nil {
		return nil, catalog.ErrNotFound
	}
	var out *catalog.Change
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var aspect, kind string
		var previous []byte
		var e entity
		err := tx.QueryRowContext(ctx, selectChange, changeID).Scan(&aspect, &previous, &e.id, &kind, &e.reference)
		if errors.Is(err, sql.ErrNoRows) {
			return catalog.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("reading change: %w", err)
		}
		e.kind = catalog.Kind(kind)
		out, err = writeAspect(ctx, tx, aspectWrite{
			entity: e, name: catalog.AspectName(aspect), value: previous, by: by, reverts: changeID,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WriteAspects writes several aspects in one transaction.
func (s *Store) WriteAspects(ctx context.Context, edits []catalog.AspectEdit, by string) ([]catalog.Change, error) {
	out := make([]catalog.Change, 0, len(edits))
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, ed := range edits {
			e, err := resolve(ctx, tx, ed.Ref)
			if err != nil {
				return fmt.Errorf("writing %s: %w", ed.Ref, err)
			}
			normalized, err := catalog.NormalizeAspect(e.kind, ed.Aspect, ed.Value)
			if err != nil {
				return fmt.Errorf("writing %s: %w", ed.Ref, err)
			}
			w := aspectWrite{entity: e, name: ed.Aspect, value: normalized, by: by}
			if ed.Checked {
				w.expect, w.checked = ed.Expect, true
			}
			c, err := writeAspect(ctx, tx, w)
			if err != nil {
				return err
			}
			out = append(out, *c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

const selectLatestChange = `
SELECT id, version, written_by, written_at FROM catalog_aspect_history
WHERE entity_id = $1 AND aspect = $2
ORDER BY version DESC LIMIT 1`

// RevertChanges rolls back several changes in one transaction, refusing them
// all when any one's aspect has been written since.
func (s *Store) RevertChanges(ctx context.Context, changeIDs []string, by string) ([]catalog.Change, error) {
	for _, id := range changeIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, catalog.ErrNotFound
		}
	}
	out := make([]catalog.Change, 0, len(changeIDs))
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range changeIDs {
			c, err := revertOne(ctx, tx, id, by)
			if err != nil {
				return err
			}
			out = append(out, *c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

const selectChangeForRevert = `
SELECT h.aspect, h.version, h.previous, e.id, e.kind, e.reference
FROM catalog_aspect_history h JOIN catalog_entities e ON e.id = h.entity_id
WHERE h.id = $1`

// revertOne restores what one change replaced, after checking under the
// entity's lock that the change is still the latest write of its aspect.
func revertOne(ctx context.Context, tx *sql.Tx, id, by string) (*catalog.Change, error) {
	var aspect, kind string
	var version int
	var previous []byte
	var e entity
	err := tx.QueryRowContext(ctx, selectChangeForRevert, id).Scan(&aspect, &version, &previous, &e.id, &kind, &e.reference)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading change: %w", err)
	}
	e.kind = catalog.Kind(kind)
	if _, err := tx.ExecContext(ctx, lockEntity, e.id); err != nil {
		return nil, fmt.Errorf("locking entity: %w", err)
	}
	var latest catalog.Change
	if err := tx.QueryRowContext(ctx, selectLatestChange, e.id, aspect).Scan(
		&latest.ID, &latest.Version, &latest.WrittenBy, &latest.WrittenAt); err != nil {
		return nil, fmt.Errorf("reading latest %s: %w", aspect, err)
	}
	if latest.Version != version {
		latest.Reference, latest.Aspect = e.reference, catalog.AspectName(aspect)
		return nil, &catalog.ConflictError{
			Change: id, Reference: e.reference, Aspect: catalog.AspectName(aspect), Newer: latest,
		}
	}
	return writeAspect(ctx, tx, aspectWrite{
		entity: e, name: catalog.AspectName(aspect), value: previous, by: by, reverts: id,
	})
}

const selectTagged = `
SELECT e.reference FROM catalog_aspects a JOIN catalog_entities e ON e.id = a.entity_id
WHERE a.aspect = 'tags' AND e.kind IN ('dataset', 'column') AND a.value @> jsonb_build_array($1::text)
ORDER BY e.reference
LIMIT $2`

// TaggedEntities returns the datasets and columns carrying a tag.
func (s *Store) TaggedEntities(ctx context.Context, tag string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, selectTagged, tag, limit)
	if err != nil {
		return nil, fmt.Errorf("listing tagged entities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("scanning tagged entity: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing tagged entities: %w", err)
	}
	return out, nil
}

// inTx runs fn in a transaction, committing when it returns nil.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// sameJSON reports whether two stored values are the same JSON, either absent
// or decoding to equal values; PostgreSQL's jsonb does not keep the text it
// was given.
func sameJSON(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// nullJSON stores an absent value as SQL NULL rather than JSON null.
func nullJSON(v []byte) any {
	if v == nil {
		return nil
	}
	return v
}

// nullString stores an empty string as SQL NULL.
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
