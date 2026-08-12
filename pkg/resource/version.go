package resource

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DefaultMaxVersions is the number of content revisions a resource keeps,
// counting the current head. A revision past the cap prunes the oldest version
// rows and their blobs. Bounded by default because a resource blob is up to
// MaxUploadBytes (100 MB) and an unbounded trail turns every edit into
// permanent storage growth.
const DefaultMaxVersions = 10

// MinMaxVersions is the floor a configured retention cap is raised to. One
// version is the head itself, so a cap below 2 keeps no history at all and
// would make the version panel a list of one row that changes under the reader.
const MinMaxVersions = 2

// Version is one recorded revision of a resource's content.
//
// It carries only what varies per revision. Filename is absent on purpose: the
// canonical URI embeds the filename and revision keeps the URI stable, so every
// version of a resource shares the resource's filename.
type Version struct {
	ResourceID    string `json:"resource_id" example:"a1b2c3d4e5f67890a1b2c3d4e5f67890"`
	Version       int    `json:"version" example:"3"`
	MIMEType      string `json:"mime_type" example:"text/markdown"`
	SizeBytes     int64  `json:"size_bytes" example:"34000"`
	S3Key         string `json:"s3_key" example:"resources/persona/data-engineer/a1b2/v3/etl-runbook.md"`
	UploaderSub   string `json:"uploader_sub" example:"550e8400-e29b-41d4-a716-446655440000"`
	UploaderEmail string `json:"uploader_email" example:"marcus.johnson@example.com"`
	// RestoredFrom names the version this revision re-promoted, or nil when the
	// revision was a fresh upload. A restore is recorded as a new head revision
	// rather than a rewind so the trail stays append-only.
	RestoredFrom *int      `json:"restored_from,omitempty" example:"1"`
	CreatedAt    time.Time `json:"created_at"`
}

// Revision is a new content revision to record: the blob that was just written
// and who wrote it. The version number is not an input — the store assigns it —
// and the resource's id, uri, filename, scope and metadata are untouched, which
// is the whole point of revising in place instead of delete-plus-re-upload.
type Revision struct {
	ResourceID    string
	MIMEType      string
	SizeBytes     int64
	S3Key         string
	UploaderSub   string
	UploaderEmail string
	// RestoredFrom names the version a restore re-promoted, or nil for a fresh
	// upload.
	RestoredFrom *int
}

// VersionStore persists the content-revision trail of a resource and moves the
// head to a new revision. The Postgres resource store implements it; it is a
// separate interface from Store so a deployment or test that only needs
// metadata CRUD is not forced to model the version trail, and so callers can
// type-assert for the capability.
//
// The trail and the head move together — a version row whose bytes no resource
// points at, or a head pointing at bytes no version row records, is a broken
// state — so AddRevision owns both writes rather than leaving the caller to
// sequence them.
type VersionStore interface {
	// AddRevision records a revision and points the resource head at its blob,
	// in one transaction. The version number is assigned inside that
	// transaction as one past the highest recorded, so two concurrent revisions
	// cannot claim the same number, and the stored row is returned.
	AddRevision(ctx context.Context, rev Revision) (*Version, error)

	// ListVersions returns every recorded revision of a resource, newest first.
	ListVersions(ctx context.Context, resourceID string) ([]Version, error)

	// GetVersion returns one recorded revision. A missing revision surfaces as
	// a wrapped sql.ErrNoRows (see IsNotFound).
	GetVersion(ctx context.Context, resourceID string, version int) (*Version, error)

	// PruneVersions deletes the oldest revisions beyond the newest keep and
	// returns the deleted rows so the caller can remove their blobs. It never
	// deletes a row whose S3 key is currently referenced by the resource head,
	// so pruning can never orphan the live content.
	PruneVersions(ctx context.Context, resourceID string, keep int) ([]Version, error)
}

// BuildRevisionS3Key constructs the S3 object key for a revision's blob. Each
// revision gets its own key under a v/<revisionID>/ segment so prior versions
// remain independently readable and pruning one never touches another's bytes.
//
// The key is keyed by an opaque revision id rather than the version number
// because the number is assigned by the database inside the insert transaction,
// after the blob has already been written; deriving the key from the number
// would require knowing it first, which is exactly the race the in-transaction
// assignment exists to avoid. Version 1 of a resource uploaded before
// versioning keeps the flat key BuildS3Key produced at create time.
func BuildRevisionS3Key(scope Scope, scopeID, resourceID, revisionID, filename string) string {
	scopeIDDir := string(ScopeGlobal)
	if scopeID != "" {
		scopeIDDir = scopeID
	}
	return fmt.Sprintf("resources/%s/%s/%s/v/%s/%s", scope, scopeIDDir, resourceID, revisionID, filename)
}

// NormalizeMaxVersions clamps a configured retention cap: a non-positive value
// selects the default, and anything below MinMaxVersions is raised to it.
func NormalizeMaxVersions(configured int) int {
	if configured <= 0 {
		return DefaultMaxVersions
	}
	if configured < MinMaxVersions {
		return MinMaxVersions
	}
	return configured
}

// --- PostgreSQL implementation ---

// versionColumns is the projection every version read shares, in the order
// scanVersion consumes.
const versionColumns = `resource_id, version, mime_type, size_bytes, s3_key,
	uploader_sub, uploader_email, restored_from, created_at`

// versionColumnsQualified is the same projection under the alias `v`, for the
// prune statement, whose join with `resources` makes six of these column names
// ambiguous on their own.
const versionColumnsQualified = `v.resource_id, v.version, v.mime_type, v.size_bytes, v.s3_key,
	v.uploader_sub, v.uploader_email, v.restored_from, v.created_at`

// lockResourceQuery takes the resource row's write lock for the duration of the
// revision transaction. It is what makes the version number safe to derive: two
// concurrent revisions of the same resource would otherwise both read the same
// MAX(version) and the loser would fail on the primary key. Behind the lock the
// second revision reads the first's committed row and takes the next number, so
// concurrent edits queue rather than collide. It also proves the resource exists
// before a version row is written for it.
const lockResourceQuery = `SELECT id FROM resources WHERE id = $1 FOR UPDATE`

// insertRevisionQuery records the revision, assigning its version number from
// the rows already present. Safe under the row lock taken above.
const insertRevisionQuery = `
	INSERT INTO resource_versions
	(resource_id, version, mime_type, size_bytes, s3_key,
	 uploader_sub, uploader_email, restored_from, created_at)
	SELECT $1,
	       COALESCE((SELECT MAX(version) FROM resource_versions WHERE resource_id = $1), 0) + 1,
	       $2, $3, $4, $5, $6, $7, $8
	RETURNING ` + versionColumns

// updateHeadQuery points the resource at the revision's blob. Both search-index
// signals are cleared, not just the embedding: the extracted content_text now
// describes bytes that are no longer the resource's, so leaving it would keep
// matching the old file's terms, and clearing content_indexed_at is what
// re-opens the content gap the index worker fills off the request path — on the
// job AddRevision enqueues, with the reconciler as the backstop (#1012, #1256).
const updateHeadQuery = `
	UPDATE resources
	   SET mime_type = $1, size_bytes = $2, s3_key = $3, updated_at = $4,
	       content_text = '', content_indexed_at = NULL,
	       embedding = NULL, embedding_model = '', embedding_text_hash = NULL
	 WHERE id = $5`

func (s *postgresStore) AddRevision(ctx context.Context, rev Revision) (*Version, error) { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning revision transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	var lockedID string
	if err := tx.QueryRowContext(ctx, lockResourceQuery, rev.ResourceID).Scan(&lockedID); err != nil {
		return nil, fmt.Errorf("locking resource %s for revision: %w", rev.ResourceID, err)
	}

	var restoredFrom sql.NullInt64
	if rev.RestoredFrom != nil {
		restoredFrom = sql.NullInt64{Int64: int64(*rev.RestoredFrom), Valid: true}
	}
	now := time.Now().UTC()

	v, err := scanVersion(tx.QueryRowContext(ctx, insertRevisionQuery,
		rev.ResourceID, rev.MIMEType, rev.SizeBytes, rev.S3Key,
		rev.UploaderSub, rev.UploaderEmail, restoredFrom, now))
	if err != nil {
		return nil, fmt.Errorf("recording resource revision: %w", err)
	}

	res, err := tx.ExecContext(ctx, updateHeadQuery,
		rev.MIMEType, rev.SizeBytes, rev.S3Key, now, rev.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("pointing resource at revision: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("resource not found: %s", rev.ResourceID)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing resource revision: %w", err)
	}
	// The revision replaced the resource's blob, so both the extracted content
	// text and the vector are gone (updateHeadQuery): the row owes a fresh
	// content extract plus embed, not just a re-embed.
	s.index.NotifyWrite(ctx, rev.ResourceID)
	return v, nil
}

func (s *postgresStore) ListVersions(ctx context.Context, resourceID string) ([]Version, error) { //nolint:revive // interface impl
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+versionColumns+` FROM resource_versions
		  WHERE resource_id = $1 ORDER BY version DESC`, resourceID)
	if err != nil {
		return nil, fmt.Errorf("listing resource versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	versions := make([]Version, 0)
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating resource version rows: %w", err)
	}
	return versions, nil
}

func (s *postgresStore) GetVersion(ctx context.Context, resourceID string, version int) (*Version, error) { //nolint:revive // interface impl
	row := s.db.QueryRowContext(ctx,
		`SELECT `+versionColumns+` FROM resource_versions
		  WHERE resource_id = $1 AND version = $2`, resourceID, version)
	return scanVersion(row)
}

func (s *postgresStore) PruneVersions(ctx context.Context, resourceID string, keep int) ([]Version, error) { //nolint:revive // interface impl
	if keep < MinMaxVersions {
		keep = MinMaxVersions
	}
	// The head's key is excluded rather than assumed absent: version 1 of a
	// resource uploaded before versioning shares its key with the head row, so
	// pruning it by row must not take the live blob with it. The caller deletes
	// the returned keys, and a key the head still points at would be a deleted
	// resource that still lists.
	//
	// Every returned column is table-qualified: joining `resources` puts
	// mime_type, size_bytes, s3_key, uploader_sub, uploader_email and created_at
	// in scope from both tables, and an unqualified RETURNING list is rejected as
	// ambiguous.
	rows, err := s.db.QueryContext(ctx, `
		DELETE FROM resource_versions v
		 USING resources r
		 WHERE v.resource_id = $1
		   AND r.id = v.resource_id
		   AND v.s3_key <> r.s3_key
		   AND v.version <= (
		       SELECT MAX(version) - $2 FROM resource_versions WHERE resource_id = $1
		   )
		RETURNING `+versionColumnsQualified, resourceID, keep)
	if err != nil {
		return nil, fmt.Errorf("pruning resource versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	pruned := make([]Version, 0)
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		pruned = append(pruned, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pruned resource version rows: %w", err)
	}
	return pruned, nil
}

// rowScanner is the scan surface shared by *sql.Row and *sql.Rows, so one
// version scanner serves the single-row read and the list/prune reads.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersion(sc rowScanner) (*Version, error) {
	var v Version
	var restoredFrom sql.NullInt64
	if err := sc.Scan(&v.ResourceID, &v.Version, &v.MIMEType, &v.SizeBytes, &v.S3Key,
		&v.UploaderSub, &v.UploaderEmail, &restoredFrom, &v.CreatedAt); err != nil {
		return nil, fmt.Errorf("scanning resource version: %w", err)
	}
	if restoredFrom.Valid {
		n := int(restoredFrom.Int64)
		v.RestoredFrom = &n
	}
	return &v, nil
}

// Verify interface compliance.
var _ VersionStore = (*postgresStore)(nil)
