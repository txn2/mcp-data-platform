package tableregister

import (
	"context"
	"sort"
	"strings"
)

// Target is where a registration on a connection would land, as Check
// reports it before anything is written (#2213).
type Target struct {
	Connection string
	Catalog    string
	Schema     string
	// Table is the persona-prefixed name, empty when no name was asked
	// about: a name derived from a file is known only once the file is.
	Table string
}

// QueryTable is the name the table is queried by, empty with no Table.
func (t Target) QueryTable() string {
	if t.Table == "" {
		return ""
	}
	return t.Catalog + "." + t.Schema + "." + t.Table
}

// Check answers whether caller may register a table named tableName on
// connection, with nothing written: the connection is granted, has a scratch
// target, accepts writes, and the name is valid and not held by somebody
// else. An empty tableName checks the connection alone. It is what a script's
// export asks before writing a file it was told to register (#2213), so a
// target that cannot be used fails the export with no output written.
func (r *Registrar) Check(ctx context.Context, caller Caller, connection, tableName string) (Target, error) {
	if !r.Available() {
		return Target{}, ErrUnavailable
	}
	target, err := r.resolveTarget(caller, connection)
	if err != nil {
		return Target{}, err
	}
	out := Target{Connection: connection, Catalog: target.Catalog, Schema: target.Schema}
	if strings.TrimSpace(tableName) == "" {
		return out, nil
	}
	slug := SlugifyTableName(tableName)
	if slug == "" {
		return Target{}, refusedf("table name %q has no letter or digit to name a table with; give another", tableName)
	}
	out.Table = PrefixedTableName(caller.Persona, slug)
	existing, err := r.deps.Store.ByName(ctx, connection, target.Catalog, target.Schema, out.Table)
	if err != nil {
		return Target{}, failedf("checking the table name", err)
	}
	if err := mayReplace(caller, existing, target, out.Table); err != nil {
		return Target{}, err
	}
	return out, nil
}

// ScratchConnections names, sorted, the connections caller can register a
// table on: granted to its persona, with a scratch target, accepting writes.
func (r *Registrar) ScratchConnections(caller Caller) []string {
	if r.deps.Connections == nil || r.deps.Trino == nil {
		return nil
	}
	var names []string
	for _, name := range r.deps.Connections() {
		if _, err := r.targetOf(caller, name); err == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// refusedTargetError is a connection refused as a registration target, carrying
// the connections the caller could have named. It matches the refusal it
// wraps under errors.Is, so every surface that classifies the refusal still
// does.
type refusedTargetError struct {
	err     error
	choices []string
}

func (e *refusedTargetError) Error() string {
	if len(e.choices) == 0 {
		return e.err.Error() + ". No connection you can reach accepts a table; ask an administrator to give one a scratch catalog and schema"
	}
	return e.err.Error() + ". Connections you can register a table on: " + strings.Join(e.choices, ", ")
}

func (e *refusedTargetError) Unwrap() error { return e.err }
