package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/pgvector/pgvector-go"
)

// tableName is the PostgreSQL table backing memory records.
// The DML patterns (INSERT INTO memory_records, FROM memory_records,
// UPDATE memory_records, DELETE FROM memory_records) are generated
// by the squirrel query builder at runtime.
const tableName = "memory_records"

// psq is the PostgreSQL statement builder with dollar placeholders.
var psq = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// postgresStore implements Store using PostgreSQL with pgvector.
type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL memory store.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

// Insert creates a new memory record.
func (s *postgresStore) Insert(ctx context.Context, record Record) error {
	entityURNs, err := json.Marshal(record.EntityURNs)
	if err != nil {
		return fmt.Errorf("marshaling entity_urns: %w", err)
	}

	relatedCols, err := json.Marshal(record.RelatedColumns)
	if err != nil {
		return fmt.Errorf("marshaling related_columns: %w", err)
	}

	metadata, err := json.Marshal(record.Metadata)
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}

	qb := psq.Insert(tableName).Columns(
		"id", "created_by", "persona", "dimension",
		"content", "category", "confidence", "source",
		"entity_urns", "related_columns", "metadata", "status",
	).Values(
		record.ID, record.CreatedBy, record.Persona, record.Dimension,
		record.Content, record.Category, record.Confidence, record.Source,
		entityURNs, relatedCols, metadata, record.Status,
	)

	if len(record.Embedding) > 0 {
		qb = psq.Insert(tableName).Columns(
			"id", "created_by", "persona", "dimension",
			"content", "category", "confidence", "source",
			"entity_urns", "related_columns", "embedding", "metadata", "status",
		).Values(
			record.ID, record.CreatedBy, record.Persona, record.Dimension,
			record.Content, record.Category, record.Confidence, record.Source,
			entityURNs, relatedCols, pgvector.NewVector(record.Embedding), metadata, record.Status,
		)
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return fmt.Errorf("building insert query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("inserting memory record: %w", err)
	}

	return nil
}

// Get retrieves a single memory record by ID.
func (s *postgresStore) Get(ctx context.Context, id string) (*Record, error) {
	query, args, err := psq.Select(recordColumns()...).
		From("memory_records").
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("building get query: %w", err)
	}

	record, err := scanRecord(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("querying memory record: %w", err)
	}

	return record, nil
}

// Update modifies fields on an existing memory record.
func (s *postgresStore) Update(ctx context.Context, id string, updates RecordUpdate) error {
	qb, hasUpdates, err := buildUpdateColumns(updates)
	if err != nil {
		return err
	}

	if !hasUpdates {
		return fmt.Errorf("no fields to update")
	}

	qb = qb.Set("updated_at", time.Now()).Where(sq.Eq{"id": id})

	query, args, err := qb.ToSql()
	if err != nil {
		return fmt.Errorf("building update query: %w", err)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating memory record: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("memory record not found: %s", id)
	}

	return nil
}

// buildUpdateColumns applies non-empty fields from RecordUpdate to a squirrel UpdateBuilder.
func buildUpdateColumns(updates RecordUpdate) (sq.UpdateBuilder, bool, error) {
	qb := psq.Update(tableName)
	hasUpdates := false

	if updates.Content != "" {
		qb = qb.Set("content", updates.Content)
		hasUpdates = true
	}
	if updates.Category != "" {
		qb = qb.Set("category", updates.Category)
		hasUpdates = true
	}
	if updates.Confidence != "" {
		qb = qb.Set("confidence", updates.Confidence)
		hasUpdates = true
	}
	if updates.Dimension != "" {
		qb = qb.Set("dimension", updates.Dimension)
		hasUpdates = true
	}
	if updates.Metadata != nil {
		meta, err := json.Marshal(updates.Metadata)
		if err != nil {
			return qb, false, fmt.Errorf("marshaling metadata: %w", err)
		}
		qb = qb.Set("metadata", meta)
		hasUpdates = true
	}
	if len(updates.Embedding) > 0 {
		qb = qb.Set("embedding", pgvector.NewVector(updates.Embedding))
		hasUpdates = true
	}

	return qb, hasUpdates, nil
}

// Delete soft-deletes a memory record by setting status to archived.
func (s *postgresStore) Delete(ctx context.Context, id string) error {
	query, args, err := psq.Update(tableName).
		Set("status", StatusArchived).
		Set("updated_at", time.Now()).
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return fmt.Errorf("building delete query: %w", err)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("archiving memory record: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("memory record not found: %s", id)
	}

	return nil
}

// List returns memory records matching the filter with pagination.
func (s *postgresStore) List(ctx context.Context, filter Filter) ([]Record, int, error) {
	// Count total.
	countQB := applyFilter(psq.Select("COUNT(*)").From(tableName), filter)
	countQuery, countArgs, err := countQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting memory records: %w", err)
	}

	// Fetch paginated results.
	limit := filter.EffectiveLimit()
	selectQB := applyFilter(psq.Select(recordColumns()...).From(tableName), filter).
		OrderBy("created_at DESC")
	if limit > 0 {
		selectQB = selectQB.Limit(uint64(limit))
	}
	if filter.Offset > 0 {
		selectQB = selectQB.Offset(uint64(filter.Offset))
	}

	selectQuery, selectArgs, err := selectQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building select query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying memory records: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var records []Record
	for rows.Next() {
		record, err := scanRecordRow(rows)
		if err != nil {
			return nil, 0, err
		}
		records = append(records, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating memory rows: %w", err)
	}

	return records, total, nil
}

// VectorSearch performs cosine similarity search over embeddings.
func (s *postgresStore) VectorSearch(ctx context.Context, query VectorQuery) ([]ScoredRecord, error) {
	sqlStr, args, err := buildVectorSearchQuery(query)
	if err != nil {
		return nil, err
	}

	// Prepend the vector parameter (position $1 in the raw SQL).
	vec := pgvector.NewVector(query.Embedding)
	allArgs := append([]any{vec}, args...)

	rows, err := s.db.QueryContext(ctx, sqlStr, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("executing vector search: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	return collectScoredRows(rows, query.MinScore)
}

// buildVectorSearchQuery constructs the SQL for cosine similarity search.
func buildVectorSearchQuery(query VectorQuery) (sqlStr string, args []any, err error) {
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	qb := psq.Select(
		append(recordColumns(), "1 - (embedding <=> $1) AS score")...,
	).From(tableName).
		Where("embedding IS NOT NULL").
		Where(sq.NotEq{"status": StatusArchived}).
		OrderBy("embedding <=> $1").
		Limit(uint64(limit))

	if query.Persona != "" {
		qb = qb.Where(sq.Eq{"persona": query.Persona})
	}
	if query.Status != "" {
		qb = qb.Where(sq.Eq{"status": query.Status})
	}

	sqlStr, args, err = qb.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("building vector search query: %w", err)
	}

	return sqlStr, args, nil
}

// collectScoredRows scans all scored rows and filters by minimum score.
func collectScoredRows(rows *sql.Rows, minScore float64) ([]ScoredRecord, error) {
	var results []ScoredRecord
	for rows.Next() {
		record, score, err := scanScoredRow(rows)
		if err != nil {
			return nil, err
		}
		if minScore > 0 && score < minScore {
			continue
		}
		results = append(results, ScoredRecord{Record: *record, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating vector search rows: %w", err)
	}

	return results, nil
}

// EntityLookup returns active memories linked to a DataHub URN.
func (s *postgresStore) EntityLookup(ctx context.Context, urn, persona string) ([]Record, error) {
	qb := psq.Select(recordColumns()...).
		From("memory_records").
		Where(sq.Expr("entity_urns @> ?::jsonb", fmt.Sprintf(`[%q]`, urn))).
		Where(sq.Eq{"status": StatusActive}).
		OrderBy("created_at DESC").
		Limit(uint64(DefaultLimit))

	if persona != "" {
		qb = qb.Where(sq.Eq{"persona": persona})
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return nil, fmt.Errorf("building entity lookup query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying entity memories: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var records []Record
	for rows.Next() {
		record, err := scanRecordRow(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating entity lookup rows: %w", err)
	}

	return records, nil
}

// MarkStale flags memory records as stale with a reason.
func (s *postgresStore) MarkStale(ctx context.Context, ids []string, reason string) error {
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	query, args, err := psq.Update(tableName).
		Set("status", StatusStale).
		Set("stale_reason", reason).
		Set("stale_at", now).
		Set("updated_at", now).
		Where(sq.Eq{"id": ids}).
		ToSql()
	if err != nil {
		return fmt.Errorf("building mark stale query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("marking records as stale: %w", err)
	}

	return nil
}

// MarkVerified updates the last_verified timestamp for records.
func (s *postgresStore) MarkVerified(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	query, args, err := psq.Update(tableName).
		Set("last_verified", now).
		Set("updated_at", now).
		Where(sq.Eq{"id": ids}).
		ToSql()
	if err != nil {
		return fmt.Errorf("building mark verified query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("marking records as verified: %w", err)
	}

	return nil
}

// Supersede marks an old record as superseded by a new one.
func (s *postgresStore) Supersede(ctx context.Context, oldID, newID string) error {
	// Get old record's metadata to add superseded_by.
	old, err := s.Get(ctx, oldID)
	if err != nil {
		return fmt.Errorf("getting old record: %w", err)
	}

	if old.Metadata == nil {
		old.Metadata = make(map[string]any)
	}
	old.Metadata["superseded_by"] = newID

	meta, err := json.Marshal(old.Metadata)
	if err != nil {
		return fmt.Errorf("marshaling metadata: %w", err)
	}

	now := time.Now()
	query, args, err := psq.Update(tableName).
		Set("status", StatusSuperseded).
		Set("metadata", meta).
		Set("updated_at", now).
		Where(sq.Eq{"id": oldID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("building supersede query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("superseding record: %w", err)
	}

	return nil
}

// applyFilter adds filter conditions to a SELECT builder.
func applyFilter(qb sq.SelectBuilder, filter Filter) sq.SelectBuilder {
	if filter.CreatedBy != "" {
		qb = qb.Where(sq.Eq{"created_by": filter.CreatedBy})
	}
	if filter.Persona != "" {
		qb = qb.Where(sq.Eq{"persona": filter.Persona})
	}
	if filter.Dimension != "" {
		qb = qb.Where(sq.Eq{"dimension": filter.Dimension})
	}
	if filter.Category != "" {
		qb = qb.Where(sq.Eq{"category": filter.Category})
	}
	if filter.Status != "" {
		qb = qb.Where(sq.Eq{"status": filter.Status})
	}
	if filter.Source != "" {
		qb = qb.Where(sq.Eq{"source": filter.Source})
	}
	if filter.EntityURN != "" {
		qb = qb.Where(sq.Expr("entity_urns @> ?::jsonb", fmt.Sprintf(`[%q]`, filter.EntityURN)))
	}
	if filter.Since != nil {
		qb = qb.Where(sq.GtOrEq{"created_at": *filter.Since})
	}
	if filter.Until != nil {
		qb = qb.Where(sq.LtOrEq{"created_at": *filter.Until})
	}
	return qb
}

// recordColumns returns the column list for memory record queries.
func recordColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "created_by", "persona", "dimension",
		"content", "category", "confidence", "source",
		"entity_urns", "related_columns", "metadata",
		"status", "stale_reason", "stale_at", "last_verified",
	}
}

// scanRecord scans a single row from QueryRow into a Record.
func scanRecord(row *sql.Row) (*Record, error) {
	var r Record
	var entityURNs, relatedCols, metadata []byte
	var staleReason sql.NullString
	var staleAt, lastVerified sql.NullTime

	err := row.Scan(
		&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy, &r.Persona, &r.Dimension,
		&r.Content, &r.Category, &r.Confidence, &r.Source,
		&entityURNs, &relatedCols, &metadata,
		&r.Status, &staleReason, &staleAt, &lastVerified,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning memory record: %w", err)
	}

	if err := unmarshalRecordJSON(&r, entityURNs, relatedCols, metadata); err != nil {
		return nil, err
	}

	applyNullables(&r, staleReason, staleAt, lastVerified)
	return &r, nil
}

// scanRecordRow scans a single row from Rows into a Record.
func scanRecordRow(rows *sql.Rows) (*Record, error) {
	var r Record
	var entityURNs, relatedCols, metadata []byte
	var staleReason sql.NullString
	var staleAt, lastVerified sql.NullTime

	err := rows.Scan(
		&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy, &r.Persona, &r.Dimension,
		&r.Content, &r.Category, &r.Confidence, &r.Source,
		&entityURNs, &relatedCols, &metadata,
		&r.Status, &staleReason, &staleAt, &lastVerified,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning memory row: %w", err)
	}

	if err := unmarshalRecordJSON(&r, entityURNs, relatedCols, metadata); err != nil {
		return nil, err
	}

	applyNullables(&r, staleReason, staleAt, lastVerified)
	return &r, nil
}

// scanScoredRow scans a row with an appended score column.
func scanScoredRow(rows *sql.Rows) (*Record, float64, error) {
	var r Record
	var entityURNs, relatedCols, metadata []byte
	var staleReason sql.NullString
	var staleAt, lastVerified sql.NullTime
	var score float64

	err := rows.Scan(
		&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy, &r.Persona, &r.Dimension,
		&r.Content, &r.Category, &r.Confidence, &r.Source,
		&entityURNs, &relatedCols, &metadata,
		&r.Status, &staleReason, &staleAt, &lastVerified,
		&score,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("scanning scored row: %w", err)
	}

	if err := unmarshalRecordJSON(&r, entityURNs, relatedCols, metadata); err != nil {
		return nil, 0, err
	}

	applyNullables(&r, staleReason, staleAt, lastVerified)
	return &r, score, nil
}

// unmarshalRecordJSON unmarshals JSON columns into Record fields.
func unmarshalRecordJSON(r *Record, entityURNs, relatedCols, metadata []byte) error {
	if err := json.Unmarshal(entityURNs, &r.EntityURNs); err != nil {
		return fmt.Errorf("unmarshaling entity_urns: %w", err)
	}
	if err := json.Unmarshal(relatedCols, &r.RelatedColumns); err != nil {
		return fmt.Errorf("unmarshaling related_columns: %w", err)
	}
	if err := json.Unmarshal(metadata, &r.Metadata); err != nil {
		return fmt.Errorf("unmarshaling metadata: %w", err)
	}
	return nil
}

// applyNullables applies nullable SQL values to a Record.
func applyNullables(r *Record, staleReason sql.NullString, staleAt, lastVerified sql.NullTime) {
	if staleReason.Valid {
		r.StaleReason = staleReason.String
	}
	if staleAt.Valid {
		r.StaleAt = &staleAt.Time
	}
	if lastVerified.Valid {
		r.LastVerified = &lastVerified.Time
	}
}

// Verify interface compliance.
var _ Store = (*postgresStore)(nil)
