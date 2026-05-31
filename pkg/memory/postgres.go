package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/pgvector/pgvector-go"
)

// tableName is the PostgreSQL table backing memory records.
// The DML patterns (INSERT INTO memory_records, FROM memory_records,
// UPDATE memory_records, DELETE FROM memory_records) are generated
// by the squirrel query builder at runtime.
const tableName = "memory_records"

// errNotFoundFmt is the format string for not-found errors.
const errNotFoundFmt = "memory record not found: %s"

// SQL column names. Defined as constants because squirrel queries
// reference them in column lists, predicates, and updates — repeating
// the literals would mean drift if a column is ever renamed.
const (
	colCreatedBy      = "created_by"
	colCategory       = "category"
	colEntityURNs     = "entity_urns"
	colCreatedAt      = "created_at"
	colDimension      = "dimension"
	colConfidence     = "confidence"
	colMetadata       = "metadata"
	colPersona        = "persona"
	colContent        = "content"
	colRelatedColumns = "related_columns"
	colSource         = "source"
	colStatus         = "status"
	colEmbedding      = "embedding"
	colEmbedModel     = "embedding_model"
	colEmbedTextHash  = "embedding_text_hash"
)

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

	columns := []string{
		"id", colCreatedBy, colPersona, colDimension,
		colContent, colCategory, colConfidence, colSource,
		colEntityURNs, colRelatedColumns, colMetadata, colStatus,
	}
	values := []any{
		record.ID, record.CreatedBy, record.Persona, record.Dimension,
		record.Content, record.Category, record.Confidence, record.Source,
		entityURNs, relatedCols, metadata, record.Status,
	}

	if len(record.Embedding) > 0 {
		columns = append(columns, colEmbedding, colEmbedModel, colEmbedTextHash)
		values = append(values,
			pgvector.NewVector(record.Embedding), record.EmbeddingModel, record.EmbeddingTextHash)
	}

	qb := psq.Insert(tableName).Columns(columns...).Values(values...)

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
		From(tableName).
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("building get query: %w", err)
	}

	record, err := scanRecord(s.db.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf(errNotFoundFmt, id)
		}
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
		return fmt.Errorf(errNotFoundFmt, id)
	}

	return nil
}

// buildUpdateColumns applies non-empty fields from RecordUpdate to a squirrel UpdateBuilder.
func buildUpdateColumns(updates RecordUpdate) (sq.UpdateBuilder, bool, error) {
	qb := psq.Update(tableName)
	hasUpdates := false

	if updates.Content != "" {
		qb = qb.Set(colContent, updates.Content)
		hasUpdates = true
	}
	if updates.Category != "" {
		qb = qb.Set(colCategory, updates.Category)
		hasUpdates = true
	}
	if updates.Confidence != "" {
		qb = qb.Set(colConfidence, updates.Confidence)
		hasUpdates = true
	}
	if updates.Dimension != "" {
		qb = qb.Set(colDimension, updates.Dimension)
		hasUpdates = true
	}
	if updates.Metadata != nil {
		meta, err := json.Marshal(updates.Metadata)
		if err != nil {
			return qb, false, fmt.Errorf("marshaling metadata: %w", err)
		}
		qb = qb.Set(colMetadata, sq.Expr("metadata || ?::jsonb", meta))
		hasUpdates = true
	}
	if len(updates.Embedding) > 0 {
		qb = qb.Set(colEmbedding, pgvector.NewVector(updates.Embedding)).
			Set(colEmbedModel, updates.EmbeddingModel).
			Set(colEmbedTextHash, updates.EmbeddingTextHash)
		hasUpdates = true
	}

	return qb, hasUpdates, nil
}

// Delete soft-deletes a memory record by setting status to archived.
func (s *postgresStore) Delete(ctx context.Context, id string) error {
	query, args, err := psq.Update(tableName).
		Set(colStatus, StatusArchived).
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
		return fmt.Errorf(errNotFoundFmt, id)
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
	selectQB := applyPagination(
		applyFilter(psq.Select(recordColumns()...).From(tableName), filter),
		filter,
	)

	selectQuery, selectArgs, err := selectQB.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("building select query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying memory records: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	records, err := collectRecordRows(rows)
	if err != nil {
		return nil, 0, err
	}

	return records, total, nil
}

// applyPagination adds ORDER BY, LIMIT, and OFFSET clauses to a query builder.
func applyPagination(qb sq.SelectBuilder, filter Filter) sq.SelectBuilder {
	orderClause := "created_at DESC"
	if filter.OrderBy != "" {
		orderClause = filter.OrderBy
	}
	qb = qb.OrderBy(orderClause)

	if limit := filter.EffectiveLimit(); limit > 0 {
		qb = qb.Limit(uint64(limit))
	}
	if filter.Offset > 0 {
		qb = qb.Offset(uint64(filter.Offset))
	}
	return qb
}

// collectRecordRows scans all rows into a slice of Record.
func collectRecordRows(rows *sql.Rows) ([]Record, error) {
	var records []Record
	for rows.Next() {
		record, err := scanRecordRow(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, *record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating memory rows: %w", err)
	}
	return records, nil
}

// VectorSearch performs cosine similarity search over embeddings.
func (s *postgresStore) VectorSearch(ctx context.Context, query VectorQuery) ([]ScoredRecord, error) {
	limit := clampStoreLimit(query.Limit)

	// Build SQL manually to avoid squirrel placeholder collision with the
	// vector parameter ($1) used in the ORDER BY and SELECT expressions.
	args := []any{pgvector.NewVector(query.Embedding)}
	paramIdx := 2

	where := "WHERE embedding IS NOT NULL AND status <> 'archived'"
	if query.Persona != "" {
		where += fmt.Sprintf(" AND persona = $%d", paramIdx)
		args = append(args, query.Persona)
		paramIdx++
	}
	if query.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", paramIdx)
		args = append(args, query.Status)
	}

	sqlStr := fmt.Sprintf( // #nosec G201 -- tableName is a constant, cols are hardcoded, where uses parameterized placeholders, limit is a sanitized int
		"SELECT %s, 1 - (embedding <=> $1) AS score FROM %s %s ORDER BY embedding <=> $1 LIMIT %d",
		rawRecordCols, tableName, where, limit,
	)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("executing vector search: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	return collectScoredRows(rows, query.MinScore)
}

// rawRecordCols is the 17-column record projection used by the raw-SQL
// search paths (VectorSearch, HybridSearch, LexicalSearch). Kept as a
// single constant so the column order stays in lockstep with the
// scanScoredRow / scanHybridRow scanners that read it. The vector,
// lexical, and fused scores are appended per query, after these columns.
const rawRecordCols = "id, created_at, updated_at, created_by, persona, dimension, " +
	"content, category, confidence, source, " +
	"entity_urns, related_columns, metadata, " +
	"status, stale_reason, stale_at, last_verified"

// ftsExpr is the Postgres full-text expression the lexical arm matches
// and ranks against. It MUST be byte-identical to the expression the
// idx_memory_records_content_fts GIN index is built on (migration
// 000054) or the planner will not use the index.
const ftsExpr = "to_tsvector('english', content)"

// ftsQuery is the parameterized tsquery the lexical predicate compares
// against. $2 is the user's query text in HybridSearch; LexicalSearch
// rebinds it to $1 (it has no vector parameter).
const ftsQuery = "plainto_tsquery('english', $2)"

// Scope-filter starting parameter indices. HybridSearch binds $1=vector
// and $2=query, so its optional persona/status predicates start at $3;
// LexicalSearch binds only $1=query, so its filters start at $2.
const (
	hybridFilterStartParam  = 3
	lexicalFilterStartParam = 2
)

// scopeFilters builds the optional persona/status predicates shared by
// the search arms, parameterized from startIdx. Returns the clause
// (prefixed with " AND " when non-empty) and the matching args so the
// caller can append them after the fixed query/vector parameters.
func scopeFilters(persona, status string, startIdx int) (clause string, args []any) {
	idx := startIdx
	if persona != "" {
		clause += fmt.Sprintf(" AND persona = $%d", idx)
		args = append(args, persona)
		idx++
	}
	if status != "" {
		clause += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, status)
	}
	return clause, args
}

// HybridSearch ranks records by fusing cosine similarity with a lexical
// full-text signal (fuseHybridScore). It runs two index-backed arms and
// fuses in Go rather than ordering by a blended SQL expression, because
// the hnsw ANN index only accelerates a pure `ORDER BY embedding <=> $1
// LIMIT k` and the GIN index only accelerates the tsquery match; a
// single blended ORDER BY would forfeit both. The vector arm returns the
// cosine top-k; the lexical arm returns the full-text top-k (including
// rows with a NULL embedding, which the vector arm cannot see). Their
// union is deduped by id (keeping the higher fused score) and sorted.
func (s *postgresStore) HybridSearch(ctx context.Context, query HybridQuery) ([]ScoredRecord, error) {
	limit := clampStoreLimit(query.Limit)
	filterClause, filterArgs := scopeFilters(query.Persona, query.Status, hybridFilterStartParam)
	args := make([]any, 0, 2+len(filterArgs))
	args = append(args, pgvector.NewVector(query.Embedding), query.QueryText)
	args = append(args, filterArgs...)

	// #nosec G201 -- tableName/cols/exprs are constants; user input
	// (vector, query text, persona, status) is parameterized; limit is a
	// sanitized int.
	vecArm := fmt.Sprintf(
		"SELECT %s, 1 - (embedding <=> $1) AS vec_score, (%s @@ %s) AS lex_match "+
			"FROM %s WHERE embedding IS NOT NULL AND status <> 'archived'%s "+
			"ORDER BY embedding <=> $1 LIMIT %d",
		rawRecordCols, ftsExpr, ftsQuery, tableName, filterClause, limit)
	lexArm := fmt.Sprintf(
		"SELECT %s, CASE WHEN embedding IS NOT NULL THEN 1 - (embedding <=> $1) ELSE 0 END AS vec_score, TRUE AS lex_match "+
			"FROM %s WHERE %s @@ %s AND status <> 'archived'%s "+
			"ORDER BY ts_rank_cd(%s, %s) DESC LIMIT %d",
		rawRecordCols, tableName, ftsExpr, ftsQuery, filterClause, ftsExpr, ftsQuery, limit)
	// #nosec G202 -- vecArm and lexArm are assembled from constant column
	// and expression strings with parameterized placeholders; no user
	// input is concatenated into the SQL.
	sqlStr := "(" + vecArm + ") UNION ALL (" + lexArm + ")"

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("executing hybrid search: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	fused, err := collectHybridRows(rows)
	if err != nil {
		return nil, err
	}
	return rankFused(fused, limit), nil
}

// LexicalSearch ranks records by Postgres full-text relevance only. It
// is the graceful-degradation path used when no embedding provider is
// available: it has no vector parameter, surfaces rows whose embedding
// is NULL, and orders by ts_rank_cd. Status defaults to active-or-stale
// (status <> 'archived') with the same optional persona/status scoping
// as the other arms.
func (s *postgresStore) LexicalSearch(ctx context.Context, query LexicalQuery) ([]ScoredRecord, error) {
	limit := clampStoreLimit(query.Limit)
	filterClause, filterArgs := scopeFilters(query.Persona, query.Status, lexicalFilterStartParam)
	args := make([]any, 0, 1+len(filterArgs))
	args = append(args, query.QueryText)
	args = append(args, filterArgs...)

	// Lexical-only: the tsquery binds to $1 (no vector parameter), so use
	// a $1-bound full-text expression rather than the $2-bound ftsQuery.
	const lexQuery = "plainto_tsquery('english', $1)"
	// #nosec G201 -- tableName/cols/exprs are constants; query text,
	// persona, status are parameterized; limit is a sanitized int.
	sqlStr := fmt.Sprintf(
		"SELECT %s, ts_rank_cd(%s, %s) AS score "+
			"FROM %s WHERE %s @@ %s AND status <> 'archived'%s "+
			"ORDER BY score DESC LIMIT %d",
		rawRecordCols, ftsExpr, lexQuery, tableName, ftsExpr, lexQuery, filterClause, limit)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("executing lexical search: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	return collectScoredRows(rows, 0)
}

// clampStoreLimit bounds a requested search limit to [1, MaxLimit],
// defaulting non-positive values to DefaultLimit.
func clampStoreLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

// hybridCandidate is one row returned by a HybridSearch arm before
// fusion: the record plus its raw cosine similarity and lexical-match
// flag.
type hybridCandidate struct {
	record   Record
	vecScore float64
	lexMatch bool
}

// collectHybridRows scans the UNION ALL result into candidates.
func collectHybridRows(rows *sql.Rows) ([]hybridCandidate, error) {
	var out []hybridCandidate
	for rows.Next() {
		c, err := scanHybridRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating hybrid search rows: %w", err)
	}
	return out, nil
}

// rankFused fuses each candidate's signals into a single score, dedups
// by record id keeping the higher score (a row can appear in both arms),
// sorts by score descending, and trims to limit. The sort is stable so
// equal-score rows keep arm order (vector arm first), which keeps the
// ranking deterministic.
func rankFused(candidates []hybridCandidate, limit int) []ScoredRecord {
	byID := make(map[string]int, len(candidates))
	var out []ScoredRecord
	for _, c := range candidates {
		score := fuseHybridScore(c.vecScore, c.lexMatch)
		if idx, ok := byID[c.record.ID]; ok {
			if score > out[idx].Score {
				out[idx].Score = score
			}
			continue
		}
		byID[c.record.ID] = len(out)
		out = append(out, ScoredRecord{Record: c.record, Score: score})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
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
	urnJSON, err := json.Marshal([]string{urn})
	if err != nil {
		return nil, fmt.Errorf("marshaling entity URN filter: %w", err)
	}

	qb := psq.Select(recordColumns()...).
		From(tableName).
		Where(sq.Expr("entity_urns @> ?::jsonb", urnJSON)).
		Where(sq.Eq{colStatus: StatusActive}).
		OrderBy("created_at DESC").
		Limit(uint64(DefaultLimit))

	if persona != "" {
		qb = qb.Where(sq.Eq{colPersona: persona})
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
		Set(colStatus, StatusStale).
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
// Uses an atomic UPDATE with jsonb || merge to avoid read-modify-write races.
func (s *postgresStore) Supersede(ctx context.Context, oldID, newID string) error {
	patch, err := json.Marshal(map[string]any{"superseded_by": newID})
	if err != nil {
		return fmt.Errorf("marshaling supersede metadata: %w", err)
	}

	now := time.Now()
	query, args, err := psq.Update(tableName).
		Set(colStatus, StatusSuperseded).
		Set(colMetadata, sq.Expr("metadata || ?::jsonb", patch)).
		Set("updated_at", now).
		Where(sq.Eq{"id": oldID}).
		ToSql()
	if err != nil {
		return fmt.Errorf("building supersede query: %w", err)
	}

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("superseding record: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf(errNotFoundFmt, oldID)
	}

	return nil
}

// applyFilter adds filter conditions to a SELECT builder.
func applyFilter(qb sq.SelectBuilder, filter Filter) sq.SelectBuilder {
	if filter.CreatedBy != "" {
		qb = qb.Where(sq.Eq{colCreatedBy: filter.CreatedBy})
	}
	if filter.Persona != "" {
		qb = qb.Where(sq.Eq{colPersona: filter.Persona})
	}
	if filter.Dimension != "" {
		qb = qb.Where(sq.Eq{colDimension: filter.Dimension})
	}
	if filter.Category != "" {
		qb = qb.Where(sq.Eq{colCategory: filter.Category})
	}
	if filter.Status != "" {
		qb = qb.Where(sq.Eq{colStatus: filter.Status})
	}
	if filter.Source != "" {
		qb = qb.Where(sq.Eq{colSource: filter.Source})
	}
	if filter.EntityURN != "" {
		urnJSON, _ := json.Marshal([]string{filter.EntityURN})
		qb = qb.Where(sq.Expr("entity_urns @> ?::jsonb", urnJSON))
	}
	if filter.Since != nil {
		qb = qb.Where(sq.GtOrEq{colCreatedAt: *filter.Since})
	}
	if filter.Until != nil {
		qb = qb.Where(sq.LtOrEq{colCreatedAt: *filter.Until})
	}
	return qb
}

// recordColumns returns the column list for memory record queries.
func recordColumns() []string {
	return []string{
		"id", colCreatedAt, "updated_at", colCreatedBy, colPersona, colDimension,
		colContent, colCategory, colConfidence, colSource,
		colEntityURNs, colRelatedColumns, colMetadata,
		colStatus, "stale_reason", "stale_at", "last_verified",
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

// scanHybridRow scans a row with appended vec_score and lex_match
// columns (the HybridSearch arms) into a candidate. The 17 record
// columns must match rawRecordCols in order.
func scanHybridRow(rows *sql.Rows) (*hybridCandidate, error) {
	var r Record
	var entityURNs, relatedCols, metadata []byte
	var staleReason sql.NullString
	var staleAt, lastVerified sql.NullTime
	var vecScore float64
	var lexMatch bool

	err := rows.Scan(
		&r.ID, &r.CreatedAt, &r.UpdatedAt, &r.CreatedBy, &r.Persona, &r.Dimension,
		&r.Content, &r.Category, &r.Confidence, &r.Source,
		&entityURNs, &relatedCols, &metadata,
		&r.Status, &staleReason, &staleAt, &lastVerified,
		&vecScore, &lexMatch,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning hybrid row: %w", err)
	}

	if err := unmarshalRecordJSON(&r, entityURNs, relatedCols, metadata); err != nil {
		return nil, err
	}

	applyNullables(&r, staleReason, staleAt, lastVerified)
	return &hybridCandidate{record: r, vecScore: vecScore, lexMatch: lexMatch}, nil
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
