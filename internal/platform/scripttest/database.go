package scripttest

import (
	"context"
	"fmt"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
)

// databaseBuiltin is testing.database(connection, schemas=None) (#2407): the
// test's statements on that application database run against a database
// built from its migrations -- in the schemas named, or every schema -- for
// this test alone, dropped when the test ends, rather than being answered
// from the recording. Fixtures are written with ordinary platform.execute
// calls.
func databaseBuiltin(dbs *scriptsession.TestDatabases) *starlark.Builtin {
	return starlark.NewBuiltin("testing.database", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var (
			connection string
			list       *starlark.List
		)
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "connection", &connection, "schemas?", &list); err != nil {
			return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
		}
		if connection == "" {
			return nil, fmt.Errorf("in %s: name the application database, as testing.database(\"orders\")", b.Name())
		}
		schemas, err := schemaNames(b, list)
		if err != nil {
			return nil, err
		}
		if err := dbs.Open(connection, schemas); err != nil {
			return nil, fmt.Errorf("in %s: %w", b.Name(), err)
		}
		return starlark.None, nil
	})
}

// schemaNames reads the schemas list; nil is every schema.
func schemaNames(b *starlark.Builtin, list *starlark.List) ([]string, error) {
	if list == nil {
		return nil, nil
	}
	out := make([]string, 0, list.Len())
	for i := range list.Len() {
		s, ok := starlark.AsString(list.Index(i))
		if !ok || s == "" {
			return nil, fmt.Errorf("in %s: schemas is a list of schema names, as schemas=[\"shop\"]", b.Name())
		}
		out = append(out, s)
	}
	return out, nil
}

// databaseCaller answers a test's calls from the recording, and its
// application-database calls from the databases it opened once it opened one.
type databaseCaller struct {
	*scriptrec.Replay
	dbs *scriptsession.TestDatabases
}

// CallTool implements scriptrun.Caller.
func (c databaseCaller) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if c.dbs.Active() && scriptsession.IsAppDBCall(name) {
		return c.dbs.Call(ctx, name, args) //nolint:wrapcheck // the database's answer, as a run's
	}
	return c.Replay.CallTool(ctx, name, args) //nolint:wrapcheck // the recording's answer, as a run's
}
