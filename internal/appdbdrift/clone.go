package appdbdrift

import (
	"context"
	"regexp"
	"strings"
)

// CloneKit is the part of the postgres toolkit a clone uses: the catalog
// read, and the build of a new schema in one transaction.
type CloneKit interface {
	Reader
	BuildSchema(ctx context.Context, connection, schema, ddl string, isolatedFrom []string) error
}

// Clone builds schema to on connection from the structure of schema from
// (#2709): its types, sequences, tables, constraints, views, functions,
// indexes, triggers, row-level security and comments, and no row. The
// baseline Render writes names every object with from; each such name is
// pointed at to, function bodies and search_path settings included, so the
// new schema's objects reach only each other. Grants are not part of the
// rendering: the platform grants the new environment's run role on to.
// BuildSchema refuses the build, keeping nothing, when to would still reach
// from. Objects the rendering cannot write are returned instead and build
// nothing.
func Clone(ctx context.Context, kit CloneKit, connection, from, to string) ([]Unsupported, error) {
	up, unsupported, err := Render(ctx, kit, connection, from)
	if err != nil || len(unsupported) > 0 {
		return unsupported, err
	}
	return nil, kit.BuildSchema(ctx, connection, to, Retarget(up, from, to), []string{from}) //nolint:wrapcheck // the toolkit's value-free summary is the text
}

// Retarget is ddl with every name qualified by schema from qualified by to
// instead: from.x and "from".x wherever they stand, and from in a function's
// SET search_path. from and to are lowercase identifiers (the toolkit
// refuses any other schema name), so neither needs quoting.
func Retarget(ddl, from, to string) string {
	qualified := regexp.MustCompile(`(^|[^A-Za-z0-9_$"])("` + regexp.QuoteMeta(from) + `"|` + regexp.QuoteMeta(from) + `)\.([A-Za-z_"])`)
	out := qualified.ReplaceAllString(ddl, "${1}"+to+".${3}")
	onPath := regexp.MustCompile(`(^|[ ,'"])` + regexp.QuoteMeta(from) + `($|[ ,'"])`)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.Contains(line, "SET search_path TO ") {
			// Twice: a match consumes the separator after it, so adjacent
			// names would otherwise be skipped.
			lines[i] = onPath.ReplaceAllString(onPath.ReplaceAllString(line, "${1}"+to+"${2}"), "${1}"+to+"${2}")
		}
	}
	return strings.Join(lines, "\n")
}
