package applicationwire

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/apphttp"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// schemasQuery lists a database's own schemas: not PostgreSQL's catalogs, and
// not mcp_schema, where the platform keeps the migration ledger.
const schemasQuery = `SELECT nspname FROM pg_namespace
WHERE nspname NOT LIKE 'pg\_%' AND nspname NOT IN ('information_schema', 'mcp_schema')
ORDER BY nspname`

// maxSchemas bounds the schemas read from one connection for the dialog.
const maxSchemas = 500

// databaseSources is what the New application dialog's choices are read from.
type databaseSources struct {
	// reach lists what a persona reaches; nil lists nothing.
	reach func(ctx context.Context, persona string, admin bool) []connreach.Connection
	// persona resolves the persona of a caller's roles, "" for none.
	persona func(roles []string) string
	// schemas reads a connection's schemas.
	schemas func(ctx context.Context, connection string) ([]string, error)
	// held lists every application, for the schemas one holds.
	held func(ctx context.Context) ([]application.Application, error)
}

// choices lists the application database connections the actor's persona
// reaches, with each one's schemas and the application holding each (#2491).
// A schema an application holds is listed even before its first release
// creates it, so the dialog never offers it as free.
func (s databaseSources) choices(r *http.Request, actor application.Actor) []apphttp.DatabaseChoice {
	ctx := r.Context()
	if s.reach == nil {
		return nil
	}
	holders := map[[2]string]string{}
	if apps, err := s.held(ctx); err != nil {
		slog.WarnContext(ctx, "application databases: reading applications failed", "error", logsan.SanitizeForLog(err.Error()))
	} else {
		for _, a := range apps {
			if a.Connection != "" {
				holders[[2]string{a.Connection, a.Schema}] = a.Name
			}
		}
	}
	out := []apphttp.DatabaseChoice{}
	for _, c := range s.reach(ctx, s.persona(actor.Roles), actor.Admin) {
		if c.Kind != postgreskit.Kind {
			continue
		}
		out = append(out, s.choice(ctx, c, holders))
	}
	return out
}

func (s databaseSources) choice(ctx context.Context, c connreach.Connection, holders map[[2]string]string) apphttp.DatabaseChoice {
	ch := apphttp.DatabaseChoice{Connection: c.Name, Description: c.Description, Schemas: []apphttp.SchemaChoice{}}
	names, err := s.schemas(ctx, c.Name)
	if err != nil {
		slog.WarnContext(ctx, "application databases: reading schemas failed",
			"connection", logsan.SanitizeForLog(c.Name), "error", logsan.SanitizeForLog(err.Error()))
		ch.Problem = "the connection's schemas could not be read; a new schema name can still be entered"
	}
	for k := range holders {
		if k[0] == c.Name && !slices.Contains(names, k[1]) {
			names = append(names, k[1])
		}
	}
	slices.Sort(names)
	for _, n := range names {
		ch.Schemas = append(ch.Schemas, apphttp.SchemaChoice{Name: n, HeldBy: holders[[2]string{c.Name, n}]})
	}
	return ch
}

// readSchemas reads a connection's schemas through the live toolkit.
func readSchemas(kit func() *postgreskit.Toolkit) func(ctx context.Context, connection string) ([]string, error) {
	return func(ctx context.Context, connection string) ([]string, error) {
		pg := kit()
		if pg == nil {
			return nil, errNoKit
		}
		res, err := pg.Query(ctx, postgreskit.Statement{Connection: connection, SQL: schemasQuery, Limit: maxSchemas})
		if err != nil {
			return nil, err //nolint:wrapcheck // logged, never shown
		}
		out := make([]string, 0, len(res.Rows))
		for _, row := range res.Rows {
			if len(row) > 0 {
				if n, ok := row[0].(string); ok {
					out = append(out, n)
				}
			}
		}
		return out, nil
	}
}
