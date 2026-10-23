// Package collitems writes and reads one collection item at a time (#2260):
// the append the asset viewer's Add to collection makes, and the summary of a
// collection's sections the picker shows. The whole-collection writes (create,
// rename, the section list replaced at once) stay in portalstore, whose
// collection store answers these two by calling here.
package collitems

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// ErrCollectionNotFound is the refusal of an append to a collection that does
// not exist or was deleted.
var ErrCollectionNotFound = errors.New("collection not found")

// AppendItem adds one asset at the end of one section (#2260). It holds the
// collection row for the transaction, so two appends to one collection, or an
// append beside a full section replace that also writes the row, take turns
// rather than interleaving positions.
func AppendItem(ctx context.Context, db *sql.DB, collectionID, sectionID, assetID string) (portaldomain.AppendedItem, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return portaldomain.AppendedItem{}, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	var locked string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM portal_collections WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		collectionID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return portaldomain.AppendedItem{}, ErrCollectionNotFound
	}
	if err != nil {
		return portaldomain.AppendedItem{}, fmt.Errorf("locking collection: %w", err)
	}

	sec, err := appendTargetSection(ctx, tx, collectionID, sectionID)
	if err != nil {
		return portaldomain.AppendedItem{}, err
	}
	item, err := insertSectionItem(ctx, tx, sec.ID, assetID)
	if err != nil {
		return portaldomain.AppendedItem{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE portal_collections SET updated_at = $1 WHERE id = $2`, time.Now(), collectionID); err != nil {
		return portaldomain.AppendedItem{}, fmt.Errorf("touching collection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return portaldomain.AppendedItem{}, fmt.Errorf("committing transaction: %w", err)
	}
	return portaldomain.AppendedItem{Item: item, SectionID: sec.ID, SectionTitle: sec.Title}, nil
}

// appendTargetSection resolves the section an append lands in: the one named,
// else the last, else a new untitled first section for a collection with none.
func appendTargetSection(ctx context.Context, tx *sql.Tx, collectionID, sectionID string) (portaldomain.SectionSummary, error) {
	var sec portaldomain.SectionSummary
	if sectionID != "" {
		err := tx.QueryRowContext(ctx,
			`SELECT id, title FROM portal_collection_sections WHERE id = $1 AND collection_id = $2`,
			sectionID, collectionID).Scan(&sec.ID, &sec.Title)
		if errors.Is(err, sql.ErrNoRows) {
			return sec, portaldomain.ErrSectionNotFound
		}
		if err != nil {
			return sec, fmt.Errorf("reading section: %w", err)
		}
		return sec, nil
	}
	err := tx.QueryRowContext(ctx,
		`SELECT id, title FROM portal_collection_sections WHERE collection_id = $1
		  ORDER BY position DESC, created_at DESC LIMIT 1`,
		collectionID).Scan(&sec.ID, &sec.Title)
	if err == nil {
		return sec, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return sec, fmt.Errorf("reading last section: %w", err)
	}
	sec.ID = uuid.New().String()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO portal_collection_sections (id, collection_id, title, description, position) VALUES ($1, $2, '', '', 0)`,
		sec.ID, collectionID); err != nil {
		return sec, fmt.Errorf("inserting first section: %w", err)
	}
	return sec, nil
}

// insertSectionItem writes assetID as the section's last item, refusing an
// asset already in the section and a section at MaxItemsPerSection.
func insertSectionItem(ctx context.Context, tx *sql.Tx, sectionID, assetID string) (portaldomain.CollectionItem, error) {
	var count, next int
	var present bool
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(position) + 1, 0), COALESCE(BOOL_OR(asset_id = $2), FALSE)
		   FROM portal_collection_items WHERE section_id = $1`,
		sectionID, assetID).Scan(&count, &next, &present); err != nil {
		return portaldomain.CollectionItem{}, fmt.Errorf("reading section items: %w", err)
	}
	if present {
		return portaldomain.CollectionItem{}, portaldomain.ErrItemAlreadyInSection
	}
	if count >= portaldomain.MaxItemsPerSection {
		return portaldomain.CollectionItem{}, portaldomain.ErrSectionFull
	}
	item := portaldomain.CollectionItem{ID: uuid.New().String(), SectionID: sectionID, AssetID: assetID, Position: next}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO portal_collection_items (id, section_id, asset_id, position) VALUES ($1, $2, $3, $4)`,
		item.ID, sectionID, assetID, next); err != nil {
		return portaldomain.CollectionItem{}, fmt.Errorf("inserting item: %w", err)
	}
	return item, nil
}

// SectionSummaries reads the sections of the given collections in one
// statement, each marked with whether assetID is among its items.
func SectionSummaries(ctx context.Context, db *sql.DB, collectionIDs []string, assetID string) (map[string][]portaldomain.SectionSummary, error) {
	out := make(map[string][]portaldomain.SectionSummary, len(collectionIDs))
	if len(collectionIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT s.collection_id, s.id, s.title,
		        COUNT(i.id), COALESCE(BOOL_OR(i.asset_id = $2), FALSE)
		   FROM portal_collection_sections s
		   LEFT JOIN portal_collection_items i ON i.section_id = s.id
		  WHERE s.collection_id = ANY($1)
		  GROUP BY s.collection_id, s.id, s.title, s.position, s.created_at
		  ORDER BY s.collection_id, s.position, s.created_at`,
		pq.Array(collectionIDs), assetID)
	if err != nil {
		return nil, fmt.Errorf("querying section summaries: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup
	for rows.Next() {
		var collID string
		var sec portaldomain.SectionSummary
		if err := rows.Scan(&collID, &sec.ID, &sec.Title, &sec.ItemCount, &sec.HasAsset); err != nil {
			return nil, fmt.Errorf("scanning section summary: %w", err)
		}
		out[collID] = append(out[collID], sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating section summaries: %w", err)
	}
	return out, nil
}
