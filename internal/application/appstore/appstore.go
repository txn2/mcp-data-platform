// Package appstore is the PostgreSQL store of applications, their parts and
// their releases (#2411): the tables of migration 000228, and the
// released_version columns it adds to scripts and portal_assets, which only
// this store writes.
package appstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/application"
)

var _ application.Store = (*Store)(nil)

// errReadParts wraps a failed read of an application's parts.
const errReadParts = "read application parts: %w"

// Constraint names PostgreSQL gives the unique indexes a write can violate.
const (
	nameConstraint   = "applications_name_key"
	schemaConstraint = "applications_schema"
	partConstraint   = "application_parts_pkey"
	releaseKey       = "application_releases_pkey"
	uniqueViolation  = "23505"
)

// Store is the PostgreSQL application store.
type Store struct {
	db *sql.DB
}

// New returns a store over db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// NewID is a new application id.
func NewID() string { return "app_" + uuid.NewString() }

const columns = `id, name, description, owner_email, connection, schema_name, current_release,
	created_by, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scan(r rowScanner) (*application.Application, error) {
	a := &application.Application{Parts: []application.Part{}}
	if err := r.Scan(&a.ID, &a.Name, &a.Description, &a.OwnerEmail, &a.Connection, &a.Schema,
		&a.CurrentRelease, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err //nolint:wrapcheck // each caller says what it was reading
	}
	return a, nil
}

// violated is the unique constraint err violates, empty for none.
func violated(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == uniqueViolation {
		return pqErr.Constraint
	}
	return ""
}

// Create inserts the application, giving it an id when it has none.
func (s *Store) Create(ctx context.Context, a *application.Application) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO applications (id, name, description, owner_email, connection, schema_name, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`,
		a.ID, a.Name, a.Description, a.OwnerEmail, a.Connection, a.Schema, a.CreatedBy,
	).Scan(&a.CreatedAt, &a.UpdatedAt)
	switch violated(err) {
	case nameConstraint:
		return application.ErrNameTaken
	case schemaConstraint:
		return application.ErrSchemaHeld
	}
	if err != nil {
		return fmt.Errorf("create application: %w", err)
	}
	if a.Parts == nil {
		a.Parts = []application.Part{}
	}
	return nil
}

// Get reads an application by id, with its parts.
func (s *Store) Get(ctx context.Context, id string) (*application.Application, error) {
	return s.one(ctx, `SELECT `+columns+` FROM applications WHERE id = $1`, id)
}

// GetByName reads an application by name, with its parts.
func (s *Store) GetByName(ctx context.Context, name string) (*application.Application, error) {
	return s.one(ctx, `SELECT `+columns+` FROM applications WHERE name = $1`, name)
}

func (s *Store) one(ctx context.Context, query, arg string) (*application.Application, error) {
	a, err := scan(s.db.QueryRowContext(ctx, query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read application: %w", err)
	}
	if a.Parts, err = s.parts(ctx, a.ID); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) parts(ctx context.Context, id string) ([]application.Part, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, part_id, added_by, added_at FROM application_parts
		 WHERE application_id = $1 ORDER BY kind, added_at, part_id`, id)
	if err != nil {
		return nil, fmt.Errorf(errReadParts, err)
	}
	defer func() { _ = rows.Close() }()
	out := []application.Part{}
	for rows.Next() {
		var p application.Part
		if err := rows.Scan(&p.Kind, &p.ID, &p.AddedBy, &p.AddedAt); err != nil {
			return nil, fmt.Errorf("read application part: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errReadParts, err)
	}
	return out, nil
}

// maxList bounds a listing.
const maxList = 500

// List reads every application by name, with its parts.
func (s *Store) List(ctx context.Context) ([]application.Application, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM applications ORDER BY name LIMIT $1`, maxList)
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []application.Application{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list applications: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	return out, s.listParts(ctx, out)
}

// listParts fills each listed application's parts, in one statement.
func (s *Store) listParts(ctx context.Context, apps []application.Application) error {
	if len(apps) == 0 {
		return nil
	}
	ids := make([]string, len(apps))
	at := make(map[string]int, len(apps))
	for i, a := range apps {
		ids[i], at[a.ID] = a.ID, i
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT application_id, kind, part_id, added_by, added_at FROM application_parts
		 WHERE application_id = ANY($1) ORDER BY kind, added_at, part_id`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("list application parts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var p application.Part
		if err := rows.Scan(&id, &p.Kind, &p.ID, &p.AddedBy, &p.AddedAt); err != nil {
			return fmt.Errorf("list application part: %w", err)
		}
		if i, ok := at[id]; ok {
			apps[i].Parts = append(apps[i].Parts, p)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list application parts: %w", err)
	}
	return nil
}

// Update writes the description and owner.
func (s *Store) Update(ctx context.Context, a *application.Application) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE applications SET description = $2, owner_email = $3, updated_at = NOW() WHERE id = $1`,
		a.ID, a.Description, a.OwnerEmail)
	if err != nil {
		return fmt.Errorf("update application: %w", err)
	}
	return affected(res)
}

func affected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return application.ErrNotFound
	}
	return nil
}

// releaseParts clears the released version of the parts given, which leave
// their application: from then on a script runs, and an asset serves, its
// newest version again. A write to the script row moves its updated_at,
// which is how a replica's compiled endpoint learns of it.
const releaseScripts = `UPDATE scripts SET released_version = NULL, updated_at = NOW()
	WHERE id::text = ANY($1) AND released_version IS NOT NULL`

const releaseAssets = `UPDATE portal_assets SET released_version = NULL, updated_at = NOW()
	WHERE id = ANY($1) AND released_version IS NOT NULL`

func clearHeld(ctx context.Context, tx *sql.Tx, scripts, assets []string) error {
	if len(scripts) > 0 {
		if _, err := tx.ExecContext(ctx, releaseScripts, pq.Array(scripts)); err != nil {
			return fmt.Errorf("release scripts: %w", err)
		}
	}
	if len(assets) > 0 {
		if _, err := tx.ExecContext(ctx, releaseAssets, pq.Array(assets)); err != nil {
			return fmt.Errorf("release assets: %w", err)
		}
	}
	return nil
}

func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// partIDs reads the ids of the application's parts of each pinned kind.
func partIDs(ctx context.Context, tx *sql.Tx, id string) (scripts, assets []string, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind, part_id FROM application_parts WHERE application_id = $1`, id)
	if err != nil {
		return nil, nil, fmt.Errorf(errReadParts, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind, part string
		if err := rows.Scan(&kind, &part); err != nil {
			return nil, nil, fmt.Errorf("read application part: %w", err)
		}
		switch kind {
		case application.KindScript:
			scripts = append(scripts, part)
		case application.KindAsset:
			assets = append(assets, part)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf(errReadParts, err)
	}
	return scripts, assets, nil
}

// Delete removes the application, its parts and releases, and clears the
// released version its parts were held at.
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		scripts, assets, err := partIDs(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := clearHeld(ctx, tx, scripts, assets); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM applications WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete application: %w", err)
		}
		return affected(res)
	})
}

// AddPart adds a part. A part another application holds is ErrPartTaken.
func (s *Store) AddPart(ctx context.Context, applicationID string, p application.Part) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO application_parts (application_id, kind, part_id, added_by) VALUES ($1, $2, $3, $4)`,
		applicationID, p.Kind, p.ID, p.AddedBy)
	if violated(err) == partConstraint {
		return application.ErrPartTaken
	}
	if err != nil {
		return fmt.Errorf("add application part: %w", err)
	}
	return nil
}

// RemovePart removes a part and clears the released version it was held at.
func (s *Store) RemovePart(ctx context.Context, applicationID, kind, id string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			DELETE FROM application_parts WHERE application_id = $1 AND kind = $2 AND part_id = $3`,
			applicationID, kind, id)
		if err != nil {
			return fmt.Errorf("remove application part: %w", err)
		}
		if err := affected(res); err != nil {
			return application.ErrNoPart
		}
		switch kind {
		case application.KindScript:
			return clearHeld(ctx, tx, []string{id}, nil)
		case application.KindAsset:
			return clearHeld(ctx, tx, nil, []string{id})
		}
		return nil
	})
}

// Owning is the application a part belongs to.
func (s *Store) Owning(ctx context.Context, kind, id string) (*application.Application, error) {
	var appID string
	err := s.db.QueryRowContext(ctx, `SELECT application_id FROM application_parts WHERE kind = $1 AND part_id = $2`,
		kind, id).Scan(&appID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, application.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read owning application: %w", err)
	}
	return s.Get(ctx, appID)
}

const releaseColumns = `number, action, migration_version, restored, changes, note, created_by, created_at`

func scanRelease(r rowScanner) (*application.Release, error) {
	rel := &application.Release{Pins: []application.Pin{}}
	var changes []byte
	if err := r.Scan(&rel.Number, &rel.Action, &rel.MigrationVersion, &rel.Restored, &changes, &rel.Note,
		&rel.CreatedBy, &rel.CreatedAt); err != nil {
		return nil, err //nolint:wrapcheck // each caller says what it was reading
	}
	if err := json.Unmarshal(changes, &rel.Changes); err != nil {
		return nil, fmt.Errorf("read release changes: %w", err)
	}
	normalizeChanges(&rel.Changes)
	return rel, nil
}

func normalizeChanges(c *application.Changes) {
	if c.Parts == nil {
		c.Parts = []application.PinChange{}
	}
	if c.Applied == nil {
		c.Applied = []int{}
	}
	if c.Reverted == nil {
		c.Reverted = []int{}
	}
}

// maxReleases bounds a listing of releases.
const maxReleases = 200

// Releases reads the application's releases, newest first, with their pins.
func (s *Store) Releases(ctx context.Context, applicationID string) ([]application.Release, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+releaseColumns+` FROM application_releases
		WHERE application_id = $1 ORDER BY number DESC LIMIT $2`, applicationID, maxReleases)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []application.Release{}
	for rows.Next() {
		rel, err := scanRelease(rows)
		if err != nil {
			return nil, fmt.Errorf("list releases: %w", err)
		}
		out = append(out, *rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	pins, err := s.allPins(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if p := pins[out[i].Number]; p != nil {
			out[i].Pins = p
		}
	}
	return out, nil
}

func (s *Store) allPins(ctx context.Context, applicationID string) (map[int][]application.Pin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT number, kind, part_id, version FROM application_release_pins
		WHERE application_id = $1 ORDER BY number, kind, part_id`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("read release pins: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int][]application.Pin{}
	for rows.Next() {
		var n int
		var p application.Pin
		if err := rows.Scan(&n, &p.Kind, &p.PartID, &p.Version); err != nil {
			return nil, fmt.Errorf("read release pin: %w", err)
		}
		out[n] = append(out[n], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read release pins: %w", err)
	}
	return out, nil
}

// Release reads one release with its pins.
func (s *Store) Release(ctx context.Context, applicationID string, number int) (*application.Release, error) {
	rel, err := scanRelease(s.db.QueryRowContext(ctx, `SELECT `+releaseColumns+` FROM application_releases
		WHERE application_id = $1 AND number = $2`, applicationID, number))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, application.ErrNoRelease
	}
	if err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	pins, err := s.allPins(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if p := pins[number]; p != nil {
		rel.Pins = p
	}
	return rel, nil
}

// Record writes a release and its pins, makes it current, and holds each
// pinned part at its version, in one transaction. The application row is
// locked and its current release compared with r.After first, so of two
// releases decided against one current release the second fails with
// ErrStale and changes nothing.
func (s *Store) Record(ctx context.Context, applicationID string, r application.Recorded) (*application.Release, error) {
	rel := r.Release
	normalizeChanges(&rel.Changes)
	if rel.Pins == nil {
		rel.Pins = []application.Pin{}
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var current int
		err := tx.QueryRowContext(ctx, `SELECT current_release FROM applications WHERE id = $1 FOR UPDATE`,
			applicationID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return application.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock application: %w", err)
		}
		if current != r.After {
			return application.ErrStale
		}
		rel.Number = current + 1
		if err := insertRelease(ctx, tx, applicationID, &rel); err != nil {
			return err
		}
		return hold(ctx, tx, applicationID, rel.Number)
	})
	if err != nil {
		return nil, err
	}
	return &rel, nil
}

func insertRelease(ctx context.Context, tx *sql.Tx, applicationID string, rel *application.Release) error {
	changes, err := json.Marshal(rel.Changes)
	if err != nil {
		return fmt.Errorf("encode release changes: %w", err)
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO application_releases (application_id, number, action, migration_version, restored, changes, note, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING created_at`,
		applicationID, rel.Number, rel.Action, rel.MigrationVersion, rel.Restored, changes, rel.Note, rel.CreatedBy,
	).Scan(&rel.CreatedAt)
	if violated(err) == releaseKey {
		return application.ErrStale
	}
	if err != nil {
		return fmt.Errorf("record release: %w", err)
	}
	for _, p := range rel.Pins {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO application_release_pins (application_id, number, kind, part_id, version) VALUES ($1, $2, $3, $4, $5)`,
			applicationID, rel.Number, p.Kind, p.PartID, p.Version); err != nil {
			return fmt.Errorf("record release pin: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE applications SET current_release = $2, updated_at = NOW() WHERE id = $1`,
		applicationID, rel.Number); err != nil {
		return fmt.Errorf("make release current: %w", err)
	}
	return nil
}

// hold sets each pinned part's released version, and clears it on the
// application's parts the release does not pin. Only a part that still
// belongs to the application is held.
func hold(ctx context.Context, tx *sql.Tx, applicationID string, number int) error {
	scripts, assets, err := partIDs(ctx, tx, applicationID)
	if err != nil {
		return err
	}
	if err := clearHeld(ctx, tx, scripts, assets); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE scripts s SET released_version = p.version, updated_at = NOW()
		  FROM application_release_pins p
		  JOIN application_parts ap ON ap.kind = p.kind AND ap.part_id = p.part_id AND ap.application_id = p.application_id
		 WHERE p.application_id = $1 AND p.number = $2 AND p.kind = 'script' AND s.id::text = p.part_id`,
		applicationID, number); err != nil {
		return fmt.Errorf("hold scripts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE portal_assets a SET released_version = p.version, updated_at = NOW()
		  FROM application_release_pins p
		  JOIN application_parts ap ON ap.kind = p.kind AND ap.part_id = p.part_id AND ap.application_id = p.application_id
		 WHERE p.application_id = $1 AND p.number = $2 AND p.kind = 'asset' AND a.id = p.part_id`,
		applicationID, number); err != nil {
		return fmt.Errorf("hold assets: %w", err)
	}
	return nil
}
