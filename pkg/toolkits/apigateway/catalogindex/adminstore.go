package catalogindex

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// AdminStore implements Store: the api-catalog-shaped queue surface
// the admin handler consumes. Enqueue / List / Get delegate to the
// generic indexjobs.Store (encoding the source_id); SpecStatuses and
// Health run joined queries against index_jobs and the api_catalog
// tables (the indexed count lives in api_catalog_operation_embeddings
// and the expected count in api_catalog_specs.operation_count, which
// the framework's generic store cannot see).
type AdminStore struct {
	jobs indexjobs.Store
	db   *sql.DB
}

// NewAdminStore returns an AdminStore backed by the generic queue
// store and the database the api_catalog tables live in.
func NewAdminStore(jobs indexjobs.Store, db *sql.DB) *AdminStore {
	return &AdminStore{jobs: jobs, db: db}
}

// Compile-time interface check.
var _ Store = (*AdminStore)(nil)

// Enqueue inserts a job for the spec, encoding the key and mapping
// the api-catalog Kind to a framework trigger.
func (s *AdminStore) Enqueue(ctx context.Context, key SpecKey, kind Kind) (bool, error) {
	created, err := s.jobs.Enqueue(ctx,
		indexjobs.Key{SourceKind: SourceKind, SourceID: EncodeSourceID(key.CatalogID, key.SpecName)},
		kind.toTrigger())
	if err != nil {
		return false, fmt.Errorf("catalogindex: enqueue: %w", err)
	}
	return created, nil
}

// List returns the matching jobs in api-catalog terms. A filter with
// a spec name targets that exact unit; a filter with only a catalog
// id matches every unit under it via the source_id prefix.
func (s *AdminStore) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	f := indexjobs.ListFilter{
		SourceKind: SourceKind,
		Status:     indexjobs.Status(filter.Status),
		Limit:      filter.Limit,
	}
	if filter.Kind != "" {
		f.Trigger = filter.Kind.toTrigger()
	}
	switch {
	case filter.CatalogID != "" && filter.SpecName != "":
		f.SourceID = EncodeSourceID(filter.CatalogID, filter.SpecName)
	case filter.CatalogID != "":
		f.SourceIDPrefix = sourceIDPrefix(filter.CatalogID)
	}
	jobs, err := s.jobs.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("catalogindex: list: %w", err)
	}
	out := make([]Job, 0, len(jobs))
	for i := range jobs {
		out = append(out, fromIndexJob(jobs[i]))
	}
	return out, nil
}

// Get returns one job by id in api-catalog terms.
func (s *AdminStore) Get(ctx context.Context, id int64) (*Job, error) {
	j, err := s.jobs.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // pass ErrNotFound through unwrapped so callers can errors.Is it
	}
	job := fromIndexJob(*j)
	return &job, nil
}

// SpecStatuses returns one row per spec in the catalog, joining
// operation_count, the persisted vector count, and the most recent
// index_jobs row for the unit. The LATERAL subquery matches the job
// row by the encoded source_id (catalog_id || delim || spec_name).
func (s *AdminStore) SpecStatuses(ctx context.Context, catalogID string) ([]SpecStatusRow, error) {
	const q = `
		SELECT s.catalog_id,
		       s.spec_name,
		       s.operation_count,
		       COALESCE(e.embedded, 0)            AS embedding_count,
		       COALESCE(j.status, '')             AS job_status,
		       COALESCE(j.attempts, 0)            AS job_attempts,
		       COALESCE(j.last_error, '')         AS job_last_error,
		       GREATEST(j.completed_at, j.started_at, j.created_at) AS job_updated_at,
		       COALESCE(j.items_done, 0)          AS items_done
		  FROM api_catalog_specs s
		  LEFT JOIN (
		    SELECT catalog_id, spec_name, COUNT(*) AS embedded
		      FROM api_catalog_operation_embeddings
		     GROUP BY catalog_id, spec_name
		  ) e USING (catalog_id, spec_name)
		  LEFT JOIN LATERAL (
		    SELECT status, attempts, last_error, created_at, started_at, completed_at, items_done
		      FROM index_jobs
		     WHERE source_kind = $2
		       AND source_id = s.catalog_id || $3 || s.spec_name
		     ORDER BY id DESC
		     LIMIT 1
		  ) j ON TRUE
		 WHERE s.catalog_id = $1
		 ORDER BY s.spec_name
	`
	rows, err := s.db.QueryContext(ctx, q, catalogID, SourceKind, sourceIDDelim)
	if err != nil {
		return nil, fmt.Errorf("catalogindex: spec statuses: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []SpecStatusRow
	for rows.Next() {
		var (
			r         SpecStatusRow
			status    string
			updatedAt sql.NullTime
		)
		if err := rows.Scan(&r.CatalogID, &r.SpecName, &r.OperationCount,
			&r.EmbeddingCount, &status, &r.JobAttempts, &r.JobLastError, &updatedAt,
			&r.EmbeddedSoFar); err != nil {
			return nil, fmt.Errorf("catalogindex: spec statuses scan: %w", err)
		}
		r.JobStatus = Status(status)
		if updatedAt.Valid {
			r.JobUpdatedAt = &updatedAt.Time
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalogindex: spec statuses rows: %w", err)
	}
	return out, nil
}

// Health is the per-catalog roll-up: total specs and how many are
// indexed / pending / running / failed, bucketed from the spec rows,
// their vector counts, and their most recent index_jobs status.
func (s *AdminStore) Health(ctx context.Context, catalogID string) (*CatalogHealth, error) {
	const q = `
		WITH spec_state AS (
		  SELECT s.catalog_id,
		         s.operation_count,
		         COALESCE(e.embedded, 0)         AS embedded,
		         COALESCE(j.status, '')          AS job_status
		    FROM api_catalog_specs s
		    LEFT JOIN (
		      SELECT catalog_id, spec_name, COUNT(*) AS embedded
		        FROM api_catalog_operation_embeddings
		       GROUP BY catalog_id, spec_name
		    ) e USING (catalog_id, spec_name)
		    LEFT JOIN LATERAL (
		      SELECT status FROM index_jobs
		       WHERE source_kind = $2
		         AND source_id = s.catalog_id || $3 || s.spec_name
		       ORDER BY id DESC LIMIT 1
		    ) j ON TRUE
		   WHERE s.catalog_id = $1
		)
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE operation_count = embedded AND operation_count > 0),
		       COUNT(*) FILTER (WHERE job_status = 'pending'),
		       COUNT(*) FILTER (WHERE job_status = 'running'),
		       COUNT(*) FILTER (WHERE job_status = 'failed')
		  FROM spec_state
	`
	h := &CatalogHealth{CatalogID: catalogID}
	if err := s.db.QueryRowContext(ctx, q, catalogID, SourceKind, sourceIDDelim).Scan(
		&h.SpecsTotal, &h.SpecsIndexed, &h.SpecsPending, &h.SpecsRunning, &h.SpecsFailed); err != nil {
		return nil, fmt.Errorf("catalogindex: health: %w", err)
	}
	return h, nil
}

// fromIndexJob maps a generic job row to the api-catalog Job shape,
// decoding the source_id and translating trigger / items_done into
// the admin vocabulary. A source_id that does not decode leaves the
// catalog id / spec name empty rather than failing the list.
func fromIndexJob(j indexjobs.Job) Job {
	catalogID, specName, _ := DecodeSourceID(j.SourceID)
	return Job{
		ID:             j.ID,
		CatalogID:      catalogID,
		SpecName:       specName,
		Kind:           kindFromTrigger(j.Trigger),
		Status:         Status(j.Status),
		Attempts:       j.Attempts,
		LastError:      j.LastError,
		NextRunAt:      j.NextRunAt,
		WorkerID:       j.WorkerID,
		LeaseExpiresAt: j.LeaseExpiresAt,
		CreatedAt:      j.CreatedAt,
		StartedAt:      j.StartedAt,
		CompletedAt:    j.CompletedAt,
		EmbeddedSoFar:  j.ItemsDone,
	}
}
