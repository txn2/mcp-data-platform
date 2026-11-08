package appwire

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epstore"
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
		Binding: func(ctx context.Context, scriptID string) (*appdiagram.Binding, error) {
			b, err := bindings.Get(ctx, scriptID)
			if errors.Is(err, epbinding.ErrNotFound) || errors.Is(err, sql.ErrNoRows) || b == nil {
				return nil, nil //nolint:nilnil // no binding is no endpoint
			}
			if err != nil {
				return nil, err //nolint:wrapcheck // the diagram leaves out what it cannot read
			}
			return &appdiagram.Binding{Name: b.Name, Path: b.Path}, nil
		},
		SchemaWrites: func(ctx context.Context, connection, schema string) []string {
			ms, err := migrations.List(ctx, connection, schema)
			if err != nil {
				return nil
			}
			var ups []string
			for _, m := range ms {
				ups = append(ups, m.Up)
			}
			return filledTables(strings.Join(ups, ";\n"))
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
