package knowledgepage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ErrRefTargetNotFound is returned when a reference points at an internal entity
// (asset, prompt, collection, page, connection) that does not exist, so the
// foreign key rejects it. Callers map it to a client error rather than a 500.
var ErrRefTargetNotFound = errors.New("entity reference target does not exist")

// pgForeignKeyViolation is the PostgreSQL SQLSTATE for a foreign-key violation.
const pgForeignKeyViolation = "23503"

// Entity-reference target types. Exactly one target is set on a reference row:
// an internal entity by foreign key, or an external DataHub URN.
const (
	RefTargetAsset         = "asset"
	RefTargetPrompt        = "prompt"
	RefTargetCollection    = "collection"
	RefTargetKnowledgePage = "knowledge_page"
	RefTargetConnection    = "connection"
	RefTargetDataHub       = "datahub"
)

// Entity-reference sources, recording how a reference came to be so the inline
// body-scan (a later phase) can reconcile only its own rows without clobbering
// picked or promoted references.
const (
	RefSourcePromoted = "promoted" // carried from a source insight by apply_knowledge
	RefSourceManual   = "manual"   // added explicitly through the authoring picker
	RefSourceInline   = "inline"   // derived from a mention in the page body
)

// EntityRef is a typed reference from a knowledge page to an entity it provides
// knowledge about. Exactly one target is populated, matching TargetType.
type EntityRef struct {
	ID             string    `json:"id,omitempty"`
	PageID         string    `json:"page_id,omitempty"`
	TargetType     string    `json:"target_type"`
	AssetID        string    `json:"asset_id,omitempty"`
	PromptID       string    `json:"prompt_id,omitempty"`
	CollectionID   string    `json:"collection_id,omitempty"`
	RefPageID      string    `json:"ref_page_id,omitempty"`
	ConnectionKind string    `json:"connection_kind,omitempty"`
	ConnectionName string    `json:"connection_name,omitempty"`
	EntityURN      string    `json:"entity_urn,omitempty"`
	Source         string    `json:"source,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// refKeySep separates the target type from its id in a reference identity key.
const refKeySep = ":"

// DataHubRef builds a reference to an external DataHub entity (a urn:li: URN).
// This is the only reference type Phase 0 writes: the references apply_knowledge
// carries from a promoted insight.
func DataHubRef(urn, source string) EntityRef {
	return EntityRef{TargetType: RefTargetDataHub, EntityURN: urn, Source: source}
}

// identity returns a stable key for a reference's target, used to de-duplicate
// within a page (the union on promotion) and mirrors each per-type unique index.
func (r EntityRef) identity() string {
	switch r.TargetType {
	case RefTargetAsset:
		return RefTargetAsset + refKeySep + r.AssetID
	case RefTargetPrompt:
		return RefTargetPrompt + refKeySep + r.PromptID
	case RefTargetCollection:
		return RefTargetCollection + refKeySep + r.CollectionID
	case RefTargetKnowledgePage:
		return RefTargetKnowledgePage + refKeySep + r.RefPageID
	case RefTargetConnection:
		return RefTargetConnection + refKeySep + r.ConnectionKind + "/" + r.ConnectionName
	case RefTargetDataHub:
		return RefTargetDataHub + refKeySep + r.EntityURN
	default:
		return r.TargetType + refKeySep
	}
}

// NewRefID returns a unique id for an entity-reference row ("kpr_<uuid>").
func NewRefID() string { return "kpr_" + uuid.New().String() }

const entityRefColumns = `id, page_id, target_type, asset_id, prompt_id, collection_id, ref_page_id, ` +
	`connection_kind, connection_name, entity_urn, source, created_by, created_at`

// ListEntityRefs returns all references of a page, oldest first.
func (s *postgresStore) ListEntityRefs(ctx context.Context, pageID string) ([]EntityRef, error) { //nolint:revive // interface impl
	query := `SELECT ` + entityRefColumns + ` FROM knowledge_page_entity_refs WHERE page_id = $1 ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, query, pageID)
	if err != nil {
		return nil, fmt.Errorf("querying entity refs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var refs []EntityRef
	for rows.Next() {
		ref, scanErr := scanEntityRef(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating entity ref rows: %w", err)
	}
	return refs, nil
}

// ValidateRefTargets checks that each foreign-key-backed reference target exists,
// so a caller (the apply_knowledge page promotion) can reject a citation to a
// missing entity BEFORE writing the page rather than after, where the insert-time
// foreign key would otherwise leave a partially written page behind. DataHub URN
// references are free text with no catalog foreign key, so they are not checked.
// Returns ErrRefTargetNotFound (wrapped with the ref identity) on the first miss.
func (s *postgresStore) ValidateRefTargets(ctx context.Context, refs []EntityRef) error { //nolint:revive // interface impl
	for i := range refs {
		ok, err := s.refTargetExists(ctx, refs[i])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("reference %q: %w", refs[i].identity(), ErrRefTargetNotFound)
		}
	}
	return nil
}

// FilterExistingRefTargets returns the subset of refs whose target exists,
// dropping the rest. It is the tolerant counterpart of ValidateRefTargets, used
// for references carried from a source insight (#690): a stale reference the
// caller cannot fix is skipped so the promotion still succeeds, rather than
// rejecting the whole apply. DataHub URN references are free text and always kept.
func (s *postgresStore) FilterExistingRefTargets(ctx context.Context, refs []EntityRef) ([]EntityRef, error) { //nolint:revive // interface impl
	kept := make([]EntityRef, 0, len(refs))
	for i := range refs {
		ok, err := s.refTargetExists(ctx, refs[i])
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, refs[i])
		}
	}
	return kept, nil
}

// refTargetExists reports whether one reference's foreign-key target row exists.
// The table set mirrors the foreign keys in migration 000073 (portal_assets,
// prompts, portal_collections, portal_knowledge_pages, connection_instances).
// Types without a catalog foreign key (datahub, unknown) are reported present.
func (s *postgresStore) refTargetExists(ctx context.Context, ref EntityRef) (bool, error) {
	var query string
	var args []any
	switch ref.TargetType {
	case RefTargetAsset:
		query, args = `SELECT 1 FROM portal_assets WHERE id = $1`, []any{ref.AssetID}
	case RefTargetPrompt:
		query, args = `SELECT 1 FROM prompts WHERE id = $1`, []any{ref.PromptID}
	case RefTargetCollection:
		query, args = `SELECT 1 FROM portal_collections WHERE id = $1`, []any{ref.CollectionID}
	case RefTargetKnowledgePage:
		query, args = `SELECT 1 FROM portal_knowledge_pages WHERE id = $1`, []any{ref.RefPageID}
	case RefTargetConnection:
		query = `SELECT 1 FROM connection_instances WHERE kind = $1 AND name = $2`
		// nosemgrep: semgrep.unbounded-make-slice-capacity -- fixed 2-element query-arg slice, not a make() with user-controlled capacity
		args = []any{ref.ConnectionKind, ref.ConnectionName}
	default:
		return true, nil // datahub URN and unknown types have no catalog FK to check
	}
	var one int
	switch err := s.db.QueryRowContext(ctx, query, args...).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("validating reference target: %w", err)
	default:
		return true, nil
	}
}

// AddEntityRefs inserts the given references for a page (union semantics): a
// reference whose target already exists on the page is skipped. Each insert uses
// ON CONFLICT DO NOTHING against the per-type unique index, so the union is
// idempotent and race-safe (a concurrent duplicate is a no-op, not an error) and
// re-running a failed promotion is safe.
func (s *postgresStore) AddEntityRefs(ctx context.Context, pageID string, refs []EntityRef) error { //nolint:revive // interface impl
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, dup := seen[ref.identity()]; dup { // collapse duplicates within the batch
			continue
		}
		seen[ref.identity()] = struct{}{}
		if err := insertEntityRef(ctx, s.db, pageID, ref); err != nil {
			return err
		}
	}
	return nil
}

// refConflictTarget returns the ON CONFLICT arbiter (matching the per-type
// partial unique index) so an insert of an already-present reference is a no-op.
func refConflictTarget(targetType string) string {
	switch targetType {
	case RefTargetAsset:
		return "(page_id, asset_id) WHERE asset_id IS NOT NULL"
	case RefTargetPrompt:
		return "(page_id, prompt_id) WHERE prompt_id IS NOT NULL"
	case RefTargetCollection:
		return "(page_id, collection_id) WHERE collection_id IS NOT NULL"
	case RefTargetKnowledgePage:
		return "(page_id, ref_page_id) WHERE ref_page_id IS NOT NULL"
	case RefTargetConnection:
		return "(page_id, connection_kind, connection_name) WHERE connection_kind IS NOT NULL"
	case RefTargetDataHub:
		return "(page_id, entity_urn) WHERE entity_urn IS NOT NULL"
	default:
		return ""
	}
}

// ReplaceEntityRefs sets a page's references to exactly the given set (clearing
// the rest), used to restore the prior references when a promotion is rolled back.
func (s *postgresStore) ReplaceEntityRefs(ctx context.Context, pageID string, refs []EntityRef) error { //nolint:revive // interface impl
	return s.replaceRefs(ctx, pageID, "", refs)
}

// ReplaceEntityRefsBySource sets the page's references of one source (for example
// the "manual" ones authored through the picker) to exactly the given set, while
// references of other sources (promoted, inline) are left untouched.
func (s *postgresStore) ReplaceEntityRefsBySource(ctx context.Context, pageID, source string, refs []EntityRef) error { //nolint:revive // interface impl
	return s.replaceRefs(ctx, pageID, source, refs)
}

// replaceRefs clears the page's references (all, or only the given source when
// non-empty) and inserts refs, in one transaction. The inserted rows are stamped
// with source when provided.
func (s *postgresStore) replaceRefs(ctx context.Context, pageID, source string, refs []EntityRef) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // commit below on success

	del := `DELETE FROM knowledge_page_entity_refs WHERE page_id = $1`
	args := []any{pageID}
	if source != "" {
		del += ` AND source = $2`
		args = append(args, source)
	}
	if _, err := tx.ExecContext(ctx, del, args...); err != nil {
		return fmt.Errorf("clearing entity refs: %w", err)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, dup := seen[ref.identity()]; dup {
			continue
		}
		if source != "" {
			ref.Source = source
		}
		if err := insertEntityRef(ctx, tx, pageID, ref); err != nil {
			return err
		}
		seen[ref.identity()] = struct{}{}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// execer is satisfied by both *sql.DB and *sql.Tx so insertEntityRef serves the
// union (DB) and replace (Tx) paths.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertEntityRef(ctx context.Context, e execer, pageID string, ref EntityRef) error {
	id := ref.ID
	if id == "" {
		id = NewRefID()
	}
	source := ref.Source
	if source == "" {
		source = RefSourcePromoted
	}
	query := `
		INSERT INTO knowledge_page_entity_refs
		(id, page_id, target_type, asset_id, prompt_id, collection_id, ref_page_id,
		 connection_kind, connection_name, entity_urn, source, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	if target := refConflictTarget(ref.TargetType); target != "" {
		query += " ON CONFLICT " + target + " DO NOTHING"
	}
	_, err := e.ExecContext(ctx, query,
		id, pageID, ref.TargetType,
		nullRefString(ref.AssetID), nullRefString(ref.PromptID), nullRefString(ref.CollectionID), nullRefString(ref.RefPageID),
		nullRefString(ref.ConnectionKind), nullRefString(ref.ConnectionName), nullRefString(ref.EntityURN),
		source, ref.CreatedBy,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && string(pqErr.Code) == pgForeignKeyViolation {
			return fmt.Errorf("reference %q: %w", ref.identity(), ErrRefTargetNotFound)
		}
		return fmt.Errorf("inserting entity ref: %w", err)
	}
	return nil
}

func scanEntityRef(rows *sql.Rows) (EntityRef, error) {
	var r EntityRef
	var assetID, promptID, collectionID, refPageID, connKind, connName, urn sql.NullString
	if err := rows.Scan(
		&r.ID, &r.PageID, &r.TargetType, &assetID, &promptID, &collectionID, &refPageID,
		&connKind, &connName, &urn, &r.Source, &r.CreatedBy, &r.CreatedAt,
	); err != nil {
		return r, fmt.Errorf("scanning entity ref row: %w", err)
	}
	r.AssetID = assetID.String
	r.PromptID = promptID.String
	r.CollectionID = collectionID.String
	r.RefPageID = refPageID.String
	r.ConnectionKind = connKind.String
	r.ConnectionName = connName.String
	r.EntityURN = urn.String
	return r, nil
}

// nullRefString maps an empty string to a SQL NULL so an unset 1-of-N target
// column is NULL (which the exactly-one CHECK and the partial unique indexes
// require), not an empty string.
func nullRefString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
