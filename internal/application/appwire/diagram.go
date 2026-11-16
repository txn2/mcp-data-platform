package appwire

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epstore"
	"github.com/txn2/mcp-data-platform/internal/pglex"
	"github.com/txn2/mcp-data-platform/internal/platform/migrationstore"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// maxDiagramContent bounds the asset text the diagram reads for references.
const maxDiagramContent = 4 << 20

// diagramDeps is what an application's diagram reads (#2540): a script's
// endpoint binding, an asset version's text, and the tables a schema's
// migrations fill.
func diagramDeps(d Deps) appsvc.DiagramDeps {
	if d.DB == nil {
		return appsvc.DiagramDeps{}
	}
	bindings, migrations := epstore.New(d.DB), migrationstore.New(d.DB)
	out := appsvc.DiagramDeps{
		Bindings: func(ctx context.Context, scriptID string) ([]appdiagram.Binding, error) {
			held, err := bindings.ByScript(ctx, scriptID)
			if err != nil {
				return nil, err //nolint:wrapcheck // the diagram leaves out what it cannot read
			}
			out := make([]appdiagram.Binding, 0, len(held))
			for _, b := range held {
				out = append(out, appdiagram.Binding{Name: b.Name, Path: b.Path})
			}
			return out, nil
		},
		SchemaTables: func(ctx context.Context, connection, schema string) appdiagram.SchemaTables {
			ms, err := migrations.List(ctx, connection, schema)
			if err != nil {
				return appdiagram.SchemaTables{}
			}
			var ups []string
			for _, m := range ms {
				ups = append(ups, m.Up)
			}
			text := strings.Join(ups, ";\n")
			return appdiagram.SchemaTables{Created: createdTables(text, schema), Filled: filledTables(text)}
		},
	}
	if d.AssetVersions != nil && d.Blobs != nil {
		out.AssetContent = assetContent(d.AssetVersions, d.Blobs)
	}
	return out
}

// assetContent reads an asset version's text from the bucket.
func assetContent(versions portaldomain.VersionReader, blobs interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
},
) func(ctx context.Context, assetID string, version int) string {
	return func(ctx context.Context, assetID string, version int) string {
		v, err := versions.GetByVersion(ctx, assetID, version)
		if err != nil || v == nil || v.SizeBytes > maxDiagramContent {
			return ""
		}
		data, _, err := blobs.GetObject(ctx, v.S3Bucket, v.S3Key)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// fillRe finds the table a statement writes rows to.
var fillRe = regexp.MustCompile(`(?i)\b(?:INSERT\s+INTO|COPY|UPDATE|MERGE\s+INTO)\s+(?:ONLY\s+)?("?[A-Za-z_][A-Za-z0-9_$]*"?(?:\."?[A-Za-z_][A-Za-z0-9_$]*"?)?)`)

// The statements that create, drop and rename a table, read at the start of
// a statement.
var (
	tableName = `("?[A-Za-z_][A-Za-z0-9_$]*"?(?:\."?[A-Za-z_][A-Za-z0-9_$]*"?)?)`
	createRe  = regexp.MustCompile(`(?is)^CREATE\s+(?:(?:GLOBAL|LOCAL)\s+)?(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` + tableName)
	dropRe    = regexp.MustCompile(`(?is)^DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?(.+?)(?:\s+(?:CASCADE|RESTRICT))?\s*$`)
	renameRe  = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?` + tableName + `\s+RENAME\s+TO\s+"?([A-Za-z_][A-Za-z0-9_$]*)"?`)
)

// createdTables is the bare names of the tables migrations leave created in
// schema, in the order they were created (#2665): each CREATE TABLE in the
// schema (a name with no qualifier is created there, the migration's search
// path), less the ones a later statement dropped, under the name a later
// rename gave. Read statement by statement, so a comment or a string that
// says CREATE TABLE is no table.
func createdTables(text, schema string) []string {
	var out []string
	for _, stmt := range pglex.Split(text) {
		out = applyTableStatement(out, strings.TrimRight(strings.TrimSpace(skipLeadingComments(stmt)), "; \t\r\n"), schema)
	}
	return out
}

// applyTableStatement is created after one statement: a table it creates in
// schema added, one it renames under its new name, the ones it drops gone.
func applyTableStatement(created []string, stmt, schema string) []string {
	if m := createRe.FindStringSubmatch(stmt); m != nil {
		if name, ok := inSchema(m[1], schema); ok && !slices.Contains(created, name) {
			created = append(created, name)
		}
		return created
	}
	if m := renameRe.FindStringSubmatch(stmt); m != nil {
		return renamed(created, m[1], m[2], schema)
	}
	if m := dropRe.FindStringSubmatch(stmt); m != nil {
		return dropped(created, m[1], schema)
	}
	return created
}

// renamed is created with the table from, in schema, under the name to.
func renamed(created []string, from, to, schema string) []string {
	if name, ok := inSchema(from, schema); ok {
		if i := slices.Index(created, name); i >= 0 {
			created[i] = to
		}
	}
	return created
}

// dropped is created without each table of a DROP TABLE list in schema.
func dropped(created []string, list, schema string) []string {
	for t := range strings.SplitSeq(list, ",") {
		if name, ok := inSchema(strings.TrimSpace(t), schema); ok {
			created = slices.DeleteFunc(created, func(n string) bool { return n == name })
		}
	}
	return created
}

// inSchema is a table name's bare name when it is in schema: unqualified, or
// qualified by schema.
func inSchema(name, schema string) (string, bool) {
	qualifier, bare, qualified := strings.Cut(name, ".")
	if !qualified {
		return strings.Trim(name, `"`), true
	}
	return strings.Trim(bare, `"`), strings.Trim(qualifier, `"`) == schema
}

// skipLeadingComments is a statement without the comments before its first
// word.
func skipLeadingComments(stmt string) string {
	for {
		trimmed := strings.TrimLeft(stmt, " \t\r\n")
		next := pglex.SkipComment(trimmed, 0)
		if next == 0 {
			return trimmed
		}
		stmt = trimmed[next:]
	}
}

// filledTables is the bare names of the tables migrations write rows to.
func filledTables(text string) []string {
	matches := fillRe.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		out = append(out, strings.Trim(name, `"`))
	}
	return out
}
