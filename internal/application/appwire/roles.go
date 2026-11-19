package appwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/script"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// errNoKit is a run-role operation with no application database toolkit
// registered.
var errNoKit = errors.New("this deployment has no application database connection kind")

// liveRoles is the live toolkit's run-role operations (#2539), looked up on
// every call so a toolkit the registry gains later is the one used.
type liveRoles struct{ reg *registry.Registry }

func (l liveRoles) kit() (*postgreskit.Toolkit, error) {
	if pg := Live(l.reg); pg != nil {
		return pg, nil
	}
	return nil, errNoKit
}

// CreateRunRole implements appsvc.Roles.
func (l liveRoles) CreateRunRole(ctx context.Context, connection, schema string) (role, password string, err error) {
	pg, err := l.kit()
	if err != nil {
		return "", "", err
	}
	return pg.CreateRunRole(ctx, connection, schema) //nolint:wrapcheck // the toolkit's refusal names the fix
}

// GrantRunRole implements appsvc.Roles.
func (l liveRoles) GrantRunRole(ctx context.Context, connection, schema string) (string, error) {
	pg, err := l.kit()
	if err != nil {
		return "", err
	}
	return pg.GrantRunRole(ctx, connection, schema) //nolint:wrapcheck // the toolkit's refusal names the fix
}

// DropRunRole implements appsvc.Roles.
func (l liveRoles) DropRunRole(ctx context.Context, connection, schema string) error {
	pg, err := l.kit()
	if err != nil {
		return err
	}
	return pg.DropRunRole(ctx, connection, schema) //nolint:wrapcheck // the toolkit's refusal names the fix
}

// members reports whether a role is a member of another on a connection,
// false when it cannot be read.
func members(reg *registry.Registry) func(ctx context.Context, connection, member, role string) bool {
	return func(ctx context.Context, connection, member, role string) bool {
		pg := Live(reg)
		if pg == nil {
			return false
		}
		ok, err := pg.IsMember(ctx, connection, member, role)
		return err == nil && ok
	}
}

// CloneSchema implements appsvc.Roles: schema to built from the structure of
// from (#2709), refused naming each object the rendering cannot write.
func (l liveRoles) CloneSchema(ctx context.Context, connection, from, to string) error {
	pg, err := l.kit()
	if err != nil {
		return err
	}
	unsupported, err := appdbdrift.Clone(ctx, appdbdrift.Toolkit{Toolkit: pg}, connection, from, to)
	if err != nil {
		return err //nolint:wrapcheck // the toolkit's refusal names the fix
	}
	if len(unsupported) > 0 {
		names := make([]string, 0, len(unsupported))
		for _, u := range unsupported {
			names = append(names, u.Kind+" "+u.Name)
		}
		return fmt.Errorf("schema %s holds objects the platform cannot copy the structure of: %s", from, strings.Join(names, ", "))
	}
	return nil
}

// libraries reads libraries by name for prepare (#2709): a library's name
// is unique among libraries.
type libraries struct {
	*scriptstore.Store
	db *sql.DB
}

// LibraryNamed is the library called name.
func (l libraries) LibraryNamed(ctx context.Context, name string) (*script.Script, error) {
	var id string
	if err := l.db.QueryRowContext(ctx, `SELECT id FROM scripts WHERE library AND name = $1`, name).Scan(&id); err != nil {
		return nil, fmt.Errorf("reading library %s: %w", name, err)
	}
	return l.GetByID(ctx, id) //nolint:wrapcheck // the store says what it read
}
