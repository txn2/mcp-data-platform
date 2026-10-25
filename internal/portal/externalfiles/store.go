package externalfiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// errOwner wraps a failure reading who a folder belongs to.
const errOwner = "reading the owner of a folder: %w"

// Person is one signed-in person and the roles the identity provider last
// stated for them, with the subject their folder is keyed by.
type Person struct {
	Email     string
	FirstName string
	LastName  string
	Roles     []string
	Subject   string
}

// Source reads the people directory.
type Source interface {
	// People lists every person who has signed in.
	People(ctx context.Context) ([]Person, error)
	// RolesOf lists the role sets of the people a user library id names: the
	// address, or the subject a person signed in as.
	RolesOf(ctx context.Context, scopeID string) ([][]string, error)
}

// Store is the Source over the users directory and the recorded subjects.
type Store struct{ db *sql.DB }

// NewStore returns the store.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const (
	peopleQuery = `SELECT u.email, u.first_name, u.last_name, u.roles, COALESCE(s.subject, '')
		FROM users u LEFT JOIN identity_subjects s ON s.address = LOWER(u.email) AND s.from_person
		WHERE u.confirmed ORDER BY LOWER(u.email)`
	rolesQuery = `SELECT u.roles FROM users u
		WHERE LOWER(u.email) = LOWER($1)
		   OR LOWER(u.email) IN (SELECT address FROM identity_subjects WHERE subject = $1 AND from_person)`
)

// People lists every person who has signed in.
func (s *Store) People(ctx context.Context) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, peopleQuery)
	if err != nil {
		return nil, fmt.Errorf("listing people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Person, 0)
	for rows.Next() {
		var p Person
		var roles []byte
		if err := rows.Scan(&p.Email, &p.FirstName, &p.LastName, &roles, &p.Subject); err != nil {
			return nil, fmt.Errorf("listing people: %w", err)
		}
		if err := json.Unmarshal(roles, &p.Roles); err != nil {
			return nil, fmt.Errorf("listing people: roles of %s: %w", p.Email, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing people: %w", err)
	}
	return out, nil
}

// RolesOf lists the role sets of the people a user library id names.
func (s *Store) RolesOf(ctx context.Context, scopeID string) ([][]string, error) {
	rows, err := s.db.QueryContext(ctx, rolesQuery, scopeID)
	if err != nil {
		return nil, fmt.Errorf(errOwner, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([][]string, 0, 1)
	for rows.Next() {
		var raw []byte
		var roles []string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf(errOwner, err)
		}
		if err := json.Unmarshal(raw, &roles); err != nil {
			return nil, fmt.Errorf(errOwner, err)
		}
		out = append(out, roles)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errOwner, err)
	}
	return out, nil
}
