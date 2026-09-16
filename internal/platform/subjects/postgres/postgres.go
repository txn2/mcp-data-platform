// Package postgres is the identity_subjects table: the subject each address
// most recently authenticated as, and whether the person themselves recorded it.
//
// It is the PostgreSQL half of internal/platform/subjects, split out under the
// module's postgres/ convention (pkg/audit/postgres, pkg/oauth/postgres) once
// the SQL grew past the point where it read as part of the Book that uses it.
// It knows nothing about that Book: it stores pairs and answers them.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Store implements subjects.Store over the identity_subjects table.
type Store struct{ db *sql.DB }

// New builds the store. A nil db yields a store that records
// nothing and knows nothing, so a caller need not check.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Record upserts the pair, keeping the subject most recently seen.
//
// A key's pair never overwrites a person's. Without that rule a service key
// configured with somebody's address would replace their subject with its own,
// and the key issued against their account would then authenticate as the
// service key instead of as them (#1759). A person's own sign-in always wins,
// which is also how a row a key wrote earlier is repaired.
func (s *Store) Record(ctx context.Context, address, subject string, fromPerson bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO identity_subjects (address, subject, seen_at, from_person)
		 VALUES ($1, $2, NOW(), $3)
		 ON CONFLICT (address) DO UPDATE
		   SET subject = EXCLUDED.subject, seen_at = NOW(), from_person = EXCLUDED.from_person
		   WHERE EXCLUDED.from_person OR NOT identity_subjects.from_person`,
		address, subject, fromPerson)
	if err != nil {
		return fmt.Errorf("recording the subject for an address: %w", err)
	}
	return nil
}

// Lookup reads the subject recorded for address.
func (s *Store) Lookup(ctx context.Context, address string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	return s.queryOne(ctx, `SELECT subject FROM identity_subjects WHERE address = $1`, address)
}

// LookupPerson reads only a pair the person themselves recorded.
func (s *Store) LookupPerson(ctx context.Context, address string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	return s.queryOne(ctx,
		`SELECT subject FROM identity_subjects WHERE address = $1 AND from_person`, address)
}

// queryOne runs a single-column subject query, answering "" for no row.
func (s *Store) queryOne(ctx context.Context, query, address string) (string, error) {
	var subject string
	err := s.db.QueryRowContext(ctx, query, address).Scan(&subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the subject for an address: %w", err)
	}
	return subject, nil
}
