package personaaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lib/pq"
)

// refsQuery reads every grant held on another object that names the persona
// ($1), in one statement. $2 is whether the persona is the administrator
// persona, whose library a webhook source naming no persona writes to.
const refsQuery = `SELECT
	COALESCE((SELECT array_agg(name ORDER BY name) FROM gateway_secrets WHERE $1 = ANY(allow_personas)), '{}'),
	(SELECT count(*) FROM gateway_secrets WHERE cardinality(allow_personas) = 0),
	(SELECT count(*) FROM script_grants WHERE principal_kind = 'persona' AND principal = $1),
	(SELECT count(*) FROM prompts WHERE scope = 'persona' AND $1 = ANY(personas)),
	(SELECT count(*) FROM portal_shares WHERE shared_with_persona = $1 AND revoked = FALSE
		AND (expires_at IS NULL OR expires_at > NOW())),
	COALESCE((SELECT array_agg(name ORDER BY name) FROM webhook_sources
		WHERE config->>'persona' = $1 OR ($2 AND COALESCE(config->>'persona', '') = '')), '{}'),
	(SELECT count(*) FROM script_endpoints WHERE auth_mode = 'signed_in' AND enabled)`

// scriptRolesQuery is the roles each script's latest applied version was
// saved with: a run presents them, so a persona they resolve to decides what
// the script can do.
const scriptRolesQuery = `SELECT DISTINCT ON (script_id) author_roles
	FROM script_versions WHERE status = 'applied' ORDER BY script_id, version DESC`

// apiKeyRolesQuery is the roles each stored API key presents.
const apiKeyRolesQuery = `SELECT roles FROM api_keys WHERE expires_at IS NULL OR expires_at > NOW()` // #nosec G101 -- a SQL statement, not a credential

// errReadingRoles wraps a failure reading a row of roles.
const errReadingRoles = "reading roles: %w"

// refScope is the persona whose references are read: its name, whether it is
// the administrator persona, whether a set of roles resolves to it (which
// counts the script versions and API keys that act as it), and the API keys
// the configuration file declares.
type refScope struct {
	name         string
	admin        bool
	resolves     func([]string) bool
	fileKeyRoles [][]string
}

// readRefs reads the references naming the persona in scope from db.
func readRefs(ctx context.Context, db *sql.DB, scope refScope) (References, Unscoped, error) {
	var refs References
	var unscoped Unscoped
	var secrets, sources pq.StringArray
	err := db.QueryRowContext(ctx, refsQuery, scope.name, scope.admin).Scan(
		&secrets, &refs.Secrets.Unrestricted, &refs.ScriptGrants, &refs.Prompts, &refs.Shares,
		&sources, &unscoped.SignedInEndpoints,
	)
	if err != nil {
		return refs, unscoped, fmt.Errorf("reading the persona's references: %w", err)
	}
	refs.Secrets.Named, refs.WebhookSources = nonNil([]string(secrets)), nonNil([]string(sources))
	if refs.ScriptVersions, err = countResolving(ctx, db, scriptRolesQuery, scope.resolves, scanTextArray); err != nil {
		return refs, unscoped, err
	}
	if refs.APIKeys, err = countResolving(ctx, db, apiKeyRolesQuery, scope.resolves, scanJSONArray); err != nil {
		return refs, unscoped, err
	}
	for _, roles := range scope.fileKeyRoles {
		if scope.resolves(roles) {
			refs.APIKeys++
		}
	}
	if refs.People, err = peopleResolving(ctx, db, scope.resolves); err != nil {
		return refs, unscoped, err
	}
	return refs, unscoped, nil
}

// peopleQuery is every signed-in person and the roles the identity provider
// last stated for them.
const peopleQuery = `SELECT email, roles FROM users WHERE confirmed ORDER BY LOWER(email)`

// peopleResolving lists the addresses of the signed-in people whose recorded
// roles resolve to the persona (#2290).
func peopleResolving(ctx context.Context, db *sql.DB, resolves func([]string) bool) ([]string, error) {
	rows, err := db.QueryContext(ctx, peopleQuery)
	if err != nil {
		return nil, fmt.Errorf(errReadingRoles, err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0)
	for rows.Next() {
		var email string
		var raw []byte
		if err := rows.Scan(&email, &raw); err != nil {
			return nil, fmt.Errorf(errReadingRoles, err)
		}
		var roles []string
		if err := json.Unmarshal(raw, &roles); err != nil {
			return nil, fmt.Errorf(errReadingRoles, err)
		}
		if resolves(roles) {
			out = append(out, email)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(errReadingRoles, err)
	}
	return out, nil
}

// countResolving counts the rows of query, each one set of roles, that
// resolve to the persona.
func countResolving(ctx context.Context, db *sql.DB, query string, resolves func([]string) bool,
	scan func(*sql.Rows) ([]string, error),
) (int, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf(errReadingRoles, err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		roles, err := scan(rows)
		if err != nil {
			return 0, err
		}
		if resolves(roles) {
			n++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf(errReadingRoles, err)
	}
	return n, nil
}

func scanTextArray(rows *sql.Rows) ([]string, error) {
	var roles pq.StringArray
	if err := rows.Scan(&roles); err != nil {
		return nil, fmt.Errorf(errReadingRoles, err)
	}
	return roles, nil
}

func scanJSONArray(rows *sql.Rows) ([]string, error) {
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return nil, fmt.Errorf(errReadingRoles, err)
	}
	var roles []string
	if err := json.Unmarshal(raw, &roles); err != nil {
		return nil, fmt.Errorf(errReadingRoles, err)
	}
	return roles, nil
}

// registrationsQuery counts the scratch tables registered on each connection.
const registrationsQuery = `SELECT connection_name, count(*) FROM table_registrations GROUP BY connection_name`

// Registrations counts the scratch tables on each connection; none without a
// database or when the count cannot be read, which the report shows as none
// registered rather than failing.
func Registrations(ctx context.Context, db *sql.DB) map[string]int {
	out := map[string]int{}
	if db == nil {
		return out
	}
	rows, err := db.QueryContext(ctx, registrationsQuery)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		var n int
		if rows.Scan(&name, &n) == nil {
			out[name] = n
		}
	}
	if rows.Err() != nil {
		return map[string]int{}
	}
	return out
}
