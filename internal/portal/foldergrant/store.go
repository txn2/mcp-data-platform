// Package foldergrant keeps write grants on Global folders (#2684): an
// administrator grants a person (by address) or a persona write on one folder
// of the Global library, and the grant covers that folder and everything
// beneath it (resource.PathUnder). A grant is a share whose target is the folder's
// path (portal_shares.folder_path), read on every request a resources check
// asks about, through the source installed in pkg/resource.
//
// Only administrators list, add and remove grants; a grantee only learns that
// they can write somewhere.
package foldergrant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/grantsource"
)

// Grant is one write grant on a Global folder.
type Grant struct {
	ID string `json:"id" example:"fg_9c1e"`
	// Folder is the Global folder the grant is on.
	Folder string `json:"folder" example:"reports"`
	// Email is the person it is for, "" for a persona grant.
	Email string `json:"email,omitempty" example:"jane@example.com"`
	// Persona is the persona it is for, "" for a person's grant.
	Persona   string    `json:"persona,omitempty" example:"finance"`
	CreatedBy string    `json:"created_by" example:"admin@example.com"`
	CreatedAt time.Time `json:"created_at"`
	// Inherited is a grant on a folder above the one asked about, which
	// covers it too.
	Inherited bool `json:"inherited"`
}

// active is a grant that still applies.
const active = `revoked = FALSE AND (expires_at IS NULL OR expires_at > NOW())`

// grantColumns are the columns a Grant is read from.
const grantColumns = `id, folder_path, COALESCE(shared_with_email, ''), shared_with_persona, created_by, created_at`

//nolint:gochecknoglobals // built once from resource.UnderSQL, the one copy of the segment rule.
var (
	// listQuery is the grants covering a folder: on it, and on every folder
	// above it.
	// #nosec G202 -- the predicate is the fixed segment rule; values are bound.
	listQuery = `SELECT ` + grantColumns + ` FROM portal_shares
		WHERE folder_path IS NOT NULL AND ` + active + ` AND ` + grantsource.UnderSQL("$1", "folder_path") + `
		ORDER BY length(folder_path), created_at`
	// folderExistsQuery is whether a Global folder exists: stored, or holding
	// a file at or beneath it.
	// #nosec G202 -- the predicate is the fixed segment rule; values are bound.
	folderExistsQuery = `SELECT EXISTS (SELECT 1 FROM resource_folders WHERE scope = 'global' AND path = $1)
		OR EXISTS (SELECT 1 FROM resources WHERE scope = 'global' AND ` + grantsource.UnderSQL("path", "$1") + `)`
	// sitesQuery is the sites serving the folder, or one above it, as media.
	// #nosec G202 -- the predicate is the fixed segment rule; values are bound.
	sitesQuery = `SELECT DISTINCT s.name FROM site_media sm JOIN sites s ON s.id = sm.site_id
		WHERE sm.scope = 'global' AND ` + grantsource.UnderSQL("$1", "sm.folder") + ` ORDER BY s.name`
	// byPersonaQuery is the grants a persona holds.
	byPersonaQuery = `SELECT ` + grantColumns + ` FROM portal_shares
		WHERE folder_path IS NOT NULL AND ` + active + ` AND shared_with_persona = $1 ORDER BY folder_path`
	// writableQuery is the folders a grantee holds a grant on: by subject,
	// by address, or by any persona their roles reach now.
	writableQuery = `SELECT DISTINCT folder_path FROM portal_shares
		WHERE folder_path IS NOT NULL AND ` + active + `
		  AND ( ($1 <> '' AND shared_with_user_id = $1)
		     OR ($2 <> '' AND LOWER(shared_with_email) = LOWER($2))
		     OR (shared_with_persona <> '' AND shared_with_persona = ANY($3)) )
		ORDER BY folder_path`
)

const (
	insertQuery = `INSERT INTO portal_shares
		(id, folder_path, token, created_by, shared_with_email, shared_with_persona, permission, origin, access_mode)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, 'editor', 'explicit', 'restricted')
		RETURNING created_at`
	duplicateQuery = `SELECT id FROM portal_shares
		WHERE folder_path = $1 AND ` + active + `
		  AND COALESCE(LOWER(shared_with_email), '') = LOWER($2) AND shared_with_persona = $3 LIMIT 1`
	removeQuery = `UPDATE portal_shares SET revoked = TRUE WHERE id = $1 AND folder_path IS NOT NULL AND revoked = FALSE
		RETURNING ` + grantColumns
)

// ErrNotFound is a grant that does not exist or was already removed.
var ErrNotFound = errors.New("folder grant not found")

// ErrDuplicate is a grant the folder already holds for the same grantee.
var ErrDuplicate = errors.New("this folder already grants write to that person or persona")

// Store reads and writes grants.
type Store struct{ db *sql.DB }

// NewStore returns the store over db, nil without one.
func NewStore(db *sql.DB) *Store {
	if db == nil {
		return nil
	}
	return &Store{db: db}
}

// List is the grants covering folder, its own first after the inherited
// ones, each marked inherited when it is on a folder above.
func (s *Store) List(ctx context.Context, folder string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, listQuery, folder)
	if err != nil {
		return nil, fmt.Errorf("listing folder grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.Folder, &g.Email, &g.Persona, &g.CreatedBy, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("reading a folder grant: %w", err)
		}
		g.Inherited = g.Folder != folder
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing folder grants: %w", err)
	}
	return out, nil
}

// ByPersona is the grants a persona holds, by folder.
func (s *Store) ByPersona(ctx context.Context, persona string) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, byPersonaQuery, persona)
	if err != nil {
		return nil, fmt.Errorf("listing a persona's folder grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.Folder, &g.Email, &g.Persona, &g.CreatedBy, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("reading a folder grant: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing a persona's folder grants: %w", err)
	}
	return out, nil
}

// FolderExists reports whether the Global folder exists.
func (s *Store) FolderExists(ctx context.Context, path string) (bool, error) {
	var ok bool
	if err := s.db.QueryRowContext(ctx, folderExistsQuery, path).Scan(&ok); err != nil {
		return false, fmt.Errorf("reading the folder: %w", err)
	}
	return ok, nil
}

// Sites is the sites serving the Global folder, or one above it, as media.
func (s *Store) Sites(ctx context.Context, path string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, sitesQuery, path)
	if err != nil {
		return nil, fmt.Errorf("reading the sites serving the folder: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("reading the sites serving the folder: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the sites serving the folder: %w", err)
	}
	return out, nil
}

// Writable is the folders the grantee holds a grant on.
func (s *Store) Writable(ctx context.Context, g grantsource.Grantee) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, writableQuery, g.Subject, g.Email, pq.Array(g.Personas))
	if err != nil {
		return nil, fmt.Errorf("reading folder grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("reading folder grants: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading folder grants: %w", err)
	}
	return out, nil
}

// Add writes a grant, refusing one the folder already holds for the grantee.
func (s *Store) Add(ctx context.Context, g *Grant) error {
	var existing string
	err := s.db.QueryRowContext(ctx, duplicateQuery, g.Folder, strings.ToLower(g.Email), g.Persona).Scan(&existing)
	switch {
	case err == nil:
		return ErrDuplicate
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("checking for the same folder grant: %w", err)
	}
	g.ID = "fg_" + randomHex(idBytes)
	if err := s.db.QueryRowContext(ctx, insertQuery, g.ID, g.Folder, "fg_"+randomHex(tokenBytes), g.CreatedBy, g.Email, g.Persona).
		Scan(&g.CreatedAt); err != nil {
		return fmt.Errorf("writing a folder grant: %w", err)
	}
	return nil
}

// Remove ends a grant, returning what it was.
func (s *Store) Remove(ctx context.Context, id string) (*Grant, error) {
	var g Grant
	err := s.db.QueryRowContext(ctx, removeQuery, id).Scan(&g.ID, &g.Folder, &g.Email, &g.Persona, &g.CreatedBy, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("removing a folder grant: %w", err)
	}
	return &g, nil
}

// The random bytes a grant's id and its unused share token carry.
const (
	idBytes    = 12
	tokenBytes = 24
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Source is the resource.FolderGrantSource the platform installs: a
// grantee's grants, none for a person any of whose personas is now external,
// since a person of an external persona never sees Global.
type Source struct {
	Store *Store
	// External reports whether a persona is external; nil reads none as one.
	External func(persona string) bool
}

// WritableFolders is resource.FolderGrantSource.
func (s Source) WritableFolders(ctx context.Context, g grantsource.Grantee) ([]string, error) {
	if s.Store == nil {
		return []string{}, nil
	}
	if s.External != nil && slices.ContainsFunc(g.Personas, s.External) {
		return []string{}, nil
	}
	return s.Store.Writable(ctx, g)
}
