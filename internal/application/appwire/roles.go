package appwire

import (
	"context"
	"errors"

	"github.com/txn2/mcp-data-platform/pkg/registry"
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
