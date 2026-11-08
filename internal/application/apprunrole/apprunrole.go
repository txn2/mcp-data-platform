// Package apprunrole answers what the application database toolkit asks
// about the applications holding its schemas (#2539): which application
// holds a schema, and an application's run role with its password, opened
// from the sealed value the platform stored when the schema was added, for
// the pool the toolkit builds and nowhere else.
package apprunrole

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/pkg/connview"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Lookups are the two the toolkit's SetApplications takes; both nil without
// a database or an encryption key, which governs no schema.
type Lookups struct {
	Holder  func(ctx context.Context, connection, schema string) (string, error)
	RunRole func(ctx context.Context, connection, application string) (role, password string, err error)
}

// New is the lookups over store, opening passwords with sealer.
func New(store application.Store, sealer appsvc.Sealer) Lookups {
	if store == nil || sealer == nil {
		return Lookups{}
	}
	h := hooks{store: store, sealer: sealer}
	return Lookups{Holder: h.holder, RunRole: h.runRole}
}

// Over is New over the platform's database; empty without one.
func Over(db *sql.DB, sealer *fieldcrypt.RestFieldEncryptor) Lookups {
	if db == nil || sealer == nil {
		return Lookups{}
	}
	return New(appstore.New(db), sealer)
}

type hooks struct {
	store  application.Store
	sealer appsvc.Sealer
}

// holder is the name of the application holding schema on connection.
func (h hooks) holder(ctx context.Context, connection, schema string) (string, error) {
	a, err := h.store.Holding(ctx, connection, schema)
	if errors.Is(err, application.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err //nolint:wrapcheck // the toolkit says what it was reading
	}
	return a.Name, nil
}

// runRole is the application's run role on connection, its password opened
// only for the binding it was sealed under; role "" for none.
func (h hooks) runRole(ctx context.Context, connection, applicationID string) (role, password string, err error) {
	rr, err := h.store.RunRoleOf(ctx, applicationID)
	if errors.Is(err, application.ErrNoRunRole) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err //nolint:wrapcheck // the toolkit says what it was reading
	}
	if rr.Connection != connection {
		return "", "", nil
	}
	password, err = h.sealer.OpenBound(appsvc.RunRoleBinding(applicationID, rr.Connection, rr.Schema, rr.Role), rr.SealedPassword)
	if err != nil {
		return "", "", fmt.Errorf("opening the run role's password: %w", err)
	}
	return rr.Role, password, nil
}

// ReachVia is the application database connections an actor may hold a schema
// on: the appdb connections their persona reaches, every one for an
// administrator.
func ReachVia(lister *connreach.Lister, personaOf func(roles []string) string) func(ctx context.Context, actor application.Actor) ([]string, error) {
	if lister == nil {
		return nil
	}
	return func(ctx context.Context, actor application.Actor) ([]string, error) {
		out := []string{}
		for _, c := range lister.ForPersona(ctx, personaOf(actor.Roles), actor.Admin) {
			if c.Kind == postgreskit.Kind {
				out = append(out, c.Name)
			}
		}
		return out, nil
	}
}

// Reach is ReachVia over the live toolkits, the persona registry and the
// stored connections.
func Reach(toolkits *registry.Registry, personas *persona.Registry, stored connview.StoreLister) func(ctx context.Context, actor application.Actor) ([]string, error) {
	lister := connreach.New(connreach.Deps{Toolkits: toolkits, Personas: personas, Stored: stored})
	return ReachVia(lister, func(roles []string) string {
		if personas == nil {
			return ""
		}
		if p, ok := personas.GetForRoles(roles); ok && p != nil {
			return p.Name
		}
		return ""
	})
}

// SessionApplications is what a script session asks about applications
// (#2539): the application whose run role a script's statements log in as,
// and the application holding a schema. Its zero value without a database.
func SessionApplications(db *sql.DB) scriptsession.Applications {
	if db == nil {
		return scriptsession.Applications{}
	}
	store := appstore.New(db)
	return scriptsession.Applications{
		RunAs: func(ctx context.Context, scriptID, connection string) string {
			a, err := store.Owning(ctx, application.KindScript, scriptID)
			if err != nil || a.Connection != connection {
				return ""
			}
			return a.ID
		},
		Holder: func(ctx context.Context, connection, schema string) (id, name string) {
			a, err := store.Holding(ctx, connection, schema)
			if err != nil {
				return "", ""
			}
			return a.ID, a.Name
		},
	}
}
