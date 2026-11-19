package appstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appenv"
)

// DefaultEnvironment is the one environment an application created with
// none named has: its releases go live as they did before environments
// (#2696).
const DefaultEnvironment = "prod"

const envColumns = `name, release, requires_approval, schema_name, updated_by, updated_at`

func scanEnvironment(r rowScanner) (application.Environment, error) {
	var e application.Environment
	err := r.Scan(&e.Name, &e.Release, &e.RequiresApproval, &e.Schema, &e.UpdatedBy, &e.UpdatedAt)
	return e, err //nolint:wrapcheck // each caller says what it was reading
}

// environments is an application's environments in promotion order.
func (s *Store) environments(ctx context.Context, id string) ([]application.Environment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+envColumns+` FROM application_environments
		 WHERE application_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("read environments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []application.Environment{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, fmt.Errorf("read environment: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read environments: %w", err)
	}
	return out, nil
}

// listEnvironments fills each listed application's environments, in one
// statement.
func (s *Store) listEnvironments(ctx context.Context, apps []application.Application) error {
	if len(apps) == 0 {
		return nil
	}
	ids := make([]string, len(apps))
	at := make(map[string]int, len(apps))
	for i, a := range apps {
		ids[i], at[a.ID] = a.ID, i
		apps[i].Environments = []application.Environment{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT application_id, `+envColumns+` FROM application_environments
		 WHERE application_id = ANY($1) ORDER BY application_id, position`, pq.Array(ids))
	if err != nil {
		return fmt.Errorf("list environments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var e application.Environment
		if err := rows.Scan(&id, &e.Name, &e.Release, &e.RequiresApproval, &e.Schema, &e.UpdatedBy, &e.UpdatedAt); err != nil {
			return fmt.Errorf("list environment: %w", err)
		}
		if i, ok := at[id]; ok {
			apps[i].Environments = append(apps[i].Environments, e)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list environments: %w", err)
	}
	return nil
}

// lockEnvironment locks the environment named name ("" for the last) and
// reads the release it runs.
func lockEnvironment(ctx context.Context, tx *sql.Tx, applicationID, name string) (env string, running int, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT name, release FROM application_environments
		 WHERE application_id = $1 AND ($2 = '' OR name = $2)
		 ORDER BY position DESC LIMIT 1 FOR UPDATE`, applicationID, name).Scan(&env, &running)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, appenv.ErrNoEnvironment
	}
	if err != nil {
		return "", 0, fmt.Errorf("lock environment: %w", err)
	}
	return env, running, nil
}

// runRelease makes m's environment run m.Number; in the last environment the
// release becomes current and its pins are held.
func runRelease(ctx context.Context, tx *sql.Tx, applicationID string, m application.Move) error {
	env, number, by := m.Environment, m.Number, m.By
	var last bool
	err := tx.QueryRowContext(ctx, `
		UPDATE application_environments e SET release = $3, updated_by = $4, updated_at = NOW()
		 WHERE e.application_id = $1 AND e.name = $2
		RETURNING e.position = (SELECT MAX(position) FROM application_environments WHERE application_id = $1)`,
		applicationID, env, number, by).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return appenv.ErrNoEnvironment
	}
	if err != nil {
		return fmt.Errorf("set environment release: %w", err)
	}
	if !last {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE applications SET current_release = $2, updated_at = NOW() WHERE id = $1`,
		applicationID, number); err != nil {
		return fmt.Errorf("make release current: %w", err)
	}
	return hold(ctx, tx, applicationID, number)
}

// SetEnvironmentRelease makes m's environment run m.Number, failing with
// ErrStale when it no longer runs m.Expect.
func (s *Store) SetEnvironmentRelease(ctx context.Context, applicationID string, m application.Move) error {
	name := m.Environment
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM applications WHERE id = $1 FOR UPDATE`, applicationID); err != nil {
			return fmt.Errorf("lock application: %w", err)
		}
		env, running, err := lockEnvironment(ctx, tx, applicationID, name)
		if err != nil {
			return err
		}
		if env != name {
			return appenv.ErrNoEnvironment
		}
		if running != m.Expect {
			return application.ErrStale
		}
		return runRelease(ctx, tx, applicationID, m)
	})
}

// AddEnvironment inserts e after the environment named after, "" for first.
func (s *Store) AddEnvironment(ctx context.Context, applicationID string, e application.Environment, after string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		at, err := positionAfter(ctx, tx, applicationID, after)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE application_environments SET position = position + 1
			 WHERE application_id = $1 AND position >= $2`, applicationID, at); err != nil {
			return fmt.Errorf("make room for environment: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO application_environments (application_id, name, position, requires_approval, schema_name, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			applicationID, e.Name, at, e.RequiresApproval, e.Schema, e.UpdatedBy)
		if violated(err) == envKey {
			return fmt.Errorf("environment %q exists: %w", e.Name, application.ErrNameTaken)
		}
		if err != nil {
			return fmt.Errorf("add environment: %w", err)
		}
		if e.Held != nil {
			return putRunRole(ctx, tx, applicationID, *e.Held)
		}
		return nil
	})
}

// positionAfter locks the application and answers the position after the
// environment named after, 0 for "".
func positionAfter(ctx context.Context, tx *sql.Tx, applicationID, after string) (int, error) {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM applications WHERE id = $1 FOR UPDATE`, applicationID); err != nil {
		return 0, fmt.Errorf("lock application: %w", err)
	}
	if after == "" {
		return 0, nil
	}
	var at int
	err := tx.QueryRowContext(ctx, `SELECT position + 1 FROM application_environments
		 WHERE application_id = $1 AND name = $2`, applicationID, after).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, appenv.ErrNoEnvironment
	}
	if err != nil {
		return 0, fmt.Errorf("read environment: %w", err)
	}
	return at, nil
}

const envKey = "application_environments_pkey"

// RemoveEnvironment removes an environment and its variables, closing the
// gap it leaves.
func (s *Store) RemoveEnvironment(ctx context.Context, applicationID, name string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var at int
		var schema string
		err := tx.QueryRowContext(ctx, `DELETE FROM application_environments
			 WHERE application_id = $1 AND name = $2 RETURNING position, schema_name`, applicationID, name).Scan(&at, &schema)
		if errors.Is(err, sql.ErrNoRows) {
			return appenv.ErrNoEnvironment
		}
		if err != nil {
			return fmt.Errorf("remove environment: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE application_environments SET position = position - 1
			 WHERE application_id = $1 AND position > $2`, applicationID, at); err != nil {
			return fmt.Errorf("close environment gap: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM application_variables
			 WHERE application_id = $1 AND environment = $2`, applicationID, name); err != nil {
			return fmt.Errorf("remove environment variables: %w", err)
		}
		// The run role of a schema no environment runs in any longer goes with
		// it; the schema itself stays until an administrator drops it (#2709).
		if _, err := tx.ExecContext(ctx, `DELETE FROM application_run_roles r
			 WHERE r.application_id = $1 AND r.schema_name = $2
			   AND r.schema_name <> (SELECT a.schema_name FROM applications a WHERE a.id = $1)
			   AND NOT EXISTS (SELECT 1 FROM application_environments e
			                    WHERE e.application_id = $1 AND e.schema_name = r.schema_name)`, applicationID, schema); err != nil {
			return fmt.Errorf("remove environment run role: %w", err)
		}
		return nil
	})
}

// SetEnvironmentApproval changes whether an agent's move into it waits.
func (s *Store) SetEnvironmentApproval(ctx context.Context, applicationID, name string, requires bool, by string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE application_environments
		   SET requires_approval = $3, updated_by = $4, updated_at = NOW()
		 WHERE application_id = $1 AND name = $2`, applicationID, name, requires, by)
	if err != nil {
		return fmt.Errorf("set environment approval: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return appenv.ErrNoEnvironment
	}
	return nil
}

// Variables is the application's variables, defaults first.
func (s *Store) Variables(ctx context.Context, applicationID string) ([]application.Variable, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT environment, name, value, updated_by, updated_at
		  FROM application_variables WHERE application_id = $1 ORDER BY name, environment`, applicationID)
	if err != nil {
		return nil, fmt.Errorf("read variables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []application.Variable{}
	for rows.Next() {
		var v application.Variable
		if err := rows.Scan(&v.Environment, &v.Name, &v.Value, &v.UpdatedBy, &v.UpdatedAt); err != nil {
			return nil, fmt.Errorf("read variable: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read variables: %w", err)
	}
	return out, nil
}

// SetVariable writes one variable.
func (s *Store) SetVariable(ctx context.Context, applicationID string, v application.Variable) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO application_variables (application_id, environment, name, value, updated_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (application_id, environment, name)
		DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		applicationID, v.Environment, v.Name, v.Value, v.UpdatedBy); err != nil {
		return fmt.Errorf("set variable: %w", err)
	}
	return nil
}

// DeleteVariable removes one variable; removing one that is not set is not
// an error.
func (s *Store) DeleteVariable(ctx context.Context, applicationID, environment, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM application_variables
		 WHERE application_id = $1 AND environment = $2 AND name = $3`, applicationID, environment, name); err != nil {
		return fmt.Errorf("delete variable: %w", err)
	}
	return nil
}

// ForScript resolves env ("" for the last) for the application holding
// scriptID, in one statement: the environment, the version its release pins
// and its variables over the defaults. A script no application holds answers
// OK false; an environment the application lacks answers ErrNoEnvironment.
func (s *Store) ForScript(ctx context.Context, scriptID, env string) (*appenv.Script, error) {
	var name sql.NullString
	var version int
	var vars []byte
	var last bool
	err := s.db.QueryRowContext(ctx, `
		SELECT e.name, COALESCE(pin.version, 0),
		       COALESCE(e.position = (SELECT MAX(position) FROM application_environments
		                               WHERE application_id = p.application_id), false),
		       COALESCE((SELECT json_object_agg(v.name, v.value) FROM (
		           SELECT DISTINCT ON (name) name, value FROM application_variables
		            WHERE application_id = p.application_id AND environment IN ('', e.name)
		            ORDER BY name, environment DESC) v), '{}'::json)
		  FROM application_parts p
		  LEFT JOIN LATERAL (
			SELECT name, release, position FROM application_environments
			 WHERE application_id = p.application_id AND ($2 = '' OR name = $2)
			 ORDER BY position DESC LIMIT 1) e ON TRUE
		  LEFT JOIN application_release_pins pin
		    ON pin.application_id = p.application_id AND pin.number = e.release
		   AND pin.kind = 'script' AND pin.part_id = p.part_id
		 WHERE p.kind = 'script' AND p.part_id = $1`, scriptID, env).Scan(&name, &version, &last, &vars)
	if errors.Is(err, sql.ErrNoRows) {
		return &appenv.Script{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve script environment: %w", err)
	}
	if !name.Valid {
		return nil, appenv.ErrNoEnvironment
	}
	out := &appenv.Script{OK: true, Environment: name.String, Version: version, Variables: map[string]string{}, Last: last}
	if err := json.Unmarshal(vars, &out.Variables); err != nil {
		return nil, fmt.Errorf("read environment variables: %w", err)
	}
	return out, nil
}

// EnvironmentNames is the environments of the application holding scriptID,
// in promotion order; empty for a script no application holds.
func (s *Store) EnvironmentNames(ctx context.Context, scriptID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.name FROM application_parts p
		  JOIN application_environments e ON e.application_id = p.application_id
		 WHERE p.kind = 'script' AND p.part_id = $1 ORDER BY e.position`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("list script environments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("list script environment: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list script environments: %w", err)
	}
	return out, nil
}
