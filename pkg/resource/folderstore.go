package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrFolderExists is returned when a folder is created at a path its library
// already holds, stored or in use.
var ErrFolderExists = errors.New("a folder already exists at that path")

// ErrFolderNotEmpty is returned when a folder that still holds resources is
// deleted. The files are deleted one at a time first, each through the route
// that removes its stored object and its versions, and the folder after them.
var ErrFolderNotEmpty = errors.New("the folder still holds resources")

// FolderStore keeps folders as rows of their own (#1872).
//
// A folder used to exist only because a resource was filed under it, which is
// why "New folder" had nothing to write and a folder vanished when its last
// file moved out. The listing is still the union of the stored rows and the
// paths in use (Store.Folders), so a row missing here hides nothing; what the
// rows add is a folder that outlives its contents.
//
// The Postgres store implements it. A store that does not leaves the folder
// create and delete routes answering 503, and a folder move rewrites the
// resources alone, as it did before folders were stored.
type FolderStore interface {
	// CreateFolder records a folder and every ancestor it lacks. It returns
	// ErrFolderExists when the library already holds the folder, as a row or
	// because a resource is filed at or beneath it.
	CreateFolder(ctx context.Context, lib ScopeFilter, path, createdBy string) error
	// DeleteFolder removes a folder and every folder beneath it. It returns
	// ErrFolderNotEmpty when a resource is filed at or beneath it, and
	// ErrFolderEmpty when the library holds no such folder.
	DeleteFolder(ctx context.Context, lib ScopeFilter, path string) error
	// FolderExists reports whether the library holds the folder as a row.
	FolderExists(ctx context.Context, lib ScopeFilter, path string) (bool, error)
	// MoveFolderTree applies a folder move in one transaction: the resources
	// it relocates, and the stored folders at and beneath from, which take the
	// same new prefix.
	MoveFolderTree(ctx context.Context, tree FolderRename, moves []Move) error
}

// Person is one person's library, as an administrator browsing every
// library sees it.
type Person struct {
	// ScopeID is the key the library is stored under: a subject, or an
	// address when the files were scoped to one.
	ScopeID string `json:"scope_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	// Email is the address the library belongs to: the scope id when it is
	// one, else the address the owner uploaded under. Empty when neither is
	// known.
	Email string `json:"email" example:"marcus.johnson@example.com"`
	// Count is the resources in the library.
	Count int `json:"count" example:"12"`
}

// PeopleLister names every person's library that holds anything. It is the
// People folder an administrator's tree shows (#1872); the platform keeps no
// roster of user libraries, so the list is read off the rows.
type PeopleLister interface {
	People(ctx context.Context) ([]Person, error)
}

// folderLibraryWhere is the predicate naming one library of resource_folders
// or resources, with the placeholders numbered from start. Both tables key
// the global library by a NULL scope_id.
func folderLibraryWhere(lib ScopeFilter, start int) (where string, args []any) {
	where, args, _ = scopeVisibilityWhere([]ScopeFilter{lib}, start)
	return where, args
}

// folderChain is the path and every ancestor of it, shallowest first.
func folderChain(path string) []string {
	parts := PathSegments(path)
	chain := make([]string, 0, len(parts))
	for i := range parts {
		chain = append(chain, strings.Join(parts[:i+1], pathSeparator))
	}
	return chain
}

// execer is what both *sql.DB and *sql.Tx offer, so the chain is recorded
// inside a move's transaction and outside any.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertFolder is the statement that records one folder, leaving an existing
// row alone.
const insertFolder = `
	INSERT INTO resource_folders (scope, scope_id, path, created_by, created_at)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT DO NOTHING`

// recordFolderChain records a path and each of its ancestors. A path already
// recorded keeps its row: the creator and creation time are the first ones.
func recordFolderChain(ctx context.Context, db execer, lib ScopeFilter, path, createdBy string) error {
	at := time.Now().UTC()
	scopeID := sql.NullString{String: lib.ScopeID, Valid: lib.ScopeID != ""}
	for _, p := range folderChain(path) {
		if _, err := db.ExecContext(ctx, insertFolder, string(lib.Scope), scopeID, p, createdBy, at); err != nil {
			return fmt.Errorf("recording folder: %w", err)
		}
	}
	return nil
}

// buildFolderRowsUnder renders the query counting what a library holds at and
// beneath a path, in one table: resources or resource_folders.
func buildFolderRowsUnder(table string, lib ScopeFilter, path string) (query string, args []any) {
	where, args := folderLibraryWhere(lib, 1)
	n := len(args)
	// #nosec G202 -- the table is one of two constants and the predicate's
	// values are bound as parameters.
	query = `SELECT COUNT(*) FROM ` + table + ` WHERE ` + where +
		fmt.Sprintf(" AND (path = $%d OR path LIKE $%d)", n+1, n+2)
	return query, append(args, path, likePrefix(path)+"/%")
}

// countUnder counts the rows of one table at and beneath a path.
func (s *postgresStore) countUnder(ctx context.Context, table string, lib ScopeFilter, path string) (int, error) {
	query, args := buildFolderRowsUnder(table, lib, path)
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting %s: %w", table, err)
	}
	return n, nil
}

// CreateFolder records a new folder and the ancestors it lacks.
func (s *postgresStore) CreateFolder(ctx context.Context, lib ScopeFilter, path, createdBy string) error { //nolint:revive // interface impl
	for _, table := range []string{"resources", "resource_folders"} {
		n, err := s.countUnder(ctx, table, lib, path)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrFolderExists
		}
	}
	return recordFolderChain(ctx, s.db, lib, path, createdBy)
}

// FolderExists reports whether a folder is stored at the path.
func (s *postgresStore) FolderExists(ctx context.Context, lib ScopeFilter, path string) (bool, error) { //nolint:revive // interface impl
	n, err := s.countUnder(ctx, "resource_folders", lib, path)
	return n > 0, err
}

// buildDeleteFolders renders the statement removing a folder and every folder
// beneath it.
func buildDeleteFolders(lib ScopeFilter, path string) (query string, args []any) {
	where, args := folderLibraryWhere(lib, 1)
	n := len(args)
	// #nosec G202 -- the predicate's values are bound as parameters.
	query = `DELETE FROM resource_folders WHERE ` + where +
		fmt.Sprintf(" AND (path = $%d OR path LIKE $%d)", n+1, n+2)
	return query, append(args, path, likePrefix(path)+"/%")
}

// DeleteFolder removes an empty folder and the folders beneath it.
func (s *postgresStore) DeleteFolder(ctx context.Context, lib ScopeFilter, path string) error { //nolint:revive // interface impl
	held, err := s.countUnder(ctx, "resources", lib, path)
	if err != nil {
		return err
	}
	if held > 0 {
		return ErrFolderNotEmpty
	}
	query, args := buildDeleteFolders(lib, path)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("deleting folder: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFolderEmpty
	}
	return nil
}

// storedFolder is one resource_folders row a move carries.
type storedFolder struct {
	path      string
	createdBy string
	createdAt time.Time
}

// buildSelectFolders renders the read of the folders at and beneath a path.
func buildSelectFolders(lib ScopeFilter, path string) (query string, args []any) {
	where, args := folderLibraryWhere(lib, 1)
	n := len(args)
	// #nosec G202 -- the predicate's values are bound as parameters.
	query = `SELECT path, created_by, created_at FROM resource_folders WHERE ` + where +
		fmt.Sprintf(" AND (path = $%d OR path LIKE $%d)", n+1, n+2)
	return query, append(args, path, likePrefix(path)+"/%")
}

// MoveFolderTree relocates the resources and the stored folders of a folder
// move in one transaction.
func (s *postgresStore) MoveFolderTree(ctx context.Context, tree FolderRename, moves []Move) error { //nolint:revive // interface impl
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning folder move: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := applyMoves(ctx, tx, moves); err != nil {
		return err
	}
	if err := moveStoredFolders(ctx, tx, tree); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		if isUniqueViolation(err) {
			return ErrURIConflict
		}
		return fmt.Errorf("committing folder move: %w", err)
	}
	for _, m := range moves {
		s.index.NotifyWrite(ctx, m.ID)
	}
	return nil
}

// moveStoredFolders gives the stored folders at and beneath tree.From the
// prefix tree.To, and records the chain tree.To sits in.
//
// The rows are read, deleted and written back rather than updated in place:
// renaming a/b to a turns a/b/b into a/b, which is a row the same statement
// has not yet moved, and a unique index is not deferred.
func moveStoredFolders(ctx context.Context, tx *sql.Tx, tree FolderRename) error {
	found, err := readStoredFolders(ctx, tx, tree)
	if err != nil {
		return err
	}

	del, delArgs := buildDeleteFolders(tree.Library, tree.From)
	if _, err := tx.ExecContext(ctx, del, delArgs...); err != nil {
		return fmt.Errorf("clearing moved folders: %w", err)
	}
	if err := recordFolderChain(ctx, tx, tree.Library, tree.To, ""); err != nil {
		return err
	}
	scopeID := sql.NullString{String: tree.Library.ScopeID, Valid: tree.Library.ScopeID != ""}
	for _, f := range found {
		to := RepointPath(f.path, tree.From, tree.To)
		if _, err := tx.ExecContext(ctx, insertFolder,
			string(tree.Library.Scope), scopeID, to, f.createdBy, f.createdAt); err != nil {
			return fmt.Errorf("writing moved folder: %w", err)
		}
	}
	return nil
}

// readStoredFolders reads the stored folders at and beneath tree.From.
func readStoredFolders(ctx context.Context, tx *sql.Tx, tree FolderRename) ([]storedFolder, error) {
	query, args := buildSelectFolders(tree.Library, tree.From)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading folders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var found []storedFolder
	for rows.Next() {
		var f storedFolder
		if err := rows.Scan(&f.path, &f.createdBy, &f.createdAt); err != nil {
			return nil, fmt.Errorf("scanning folder: %w", err)
		}
		found = append(found, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating folders: %w", err)
	}
	return found, nil
}

// selectPeople reads every user library holding a resource or a folder, and
// the address each belongs to where the rows say.
const selectPeople = `
	SELECT scope_id, SUM(n) AS count, MAX(email) AS email FROM (
		SELECT scope_id, 1 AS n,
		       CASE WHEN uploader_sub = scope_id THEN uploader_email END AS email
		FROM resources WHERE scope = 'user' AND scope_id IS NOT NULL
		UNION ALL
		SELECT scope_id, 0, NULL FROM resource_folders WHERE scope = 'user' AND scope_id IS NOT NULL
	) AS libraries
	GROUP BY scope_id
	ORDER BY scope_id`

// People lists every person's library that holds anything.
func (s *postgresStore) People(ctx context.Context) ([]Person, error) { //nolint:revive // interface impl
	rows, err := s.db.QueryContext(ctx, selectPeople)
	if err != nil {
		return nil, fmt.Errorf("listing people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	people := make([]Person, 0)
	for rows.Next() {
		var p Person
		var email sql.NullString
		if err := rows.Scan(&p.ScopeID, &p.Count, &email); err != nil {
			return nil, fmt.Errorf("scanning person: %w", err)
		}
		p.Email = email.String
		if strings.Contains(p.ScopeID, "@") {
			p.Email = p.ScopeID
		}
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating people: %w", err)
	}
	return people, nil
}
