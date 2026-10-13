// Package catalogwire assembles the platform catalog's scan (#1918) from the
// running platform: the catalog store, the scan's settings, a source for each
// kind of connection read through its live toolkits, and the loop that runs
// the scan on its interval. The composition root starts it once the toolkits
// are wired and stops it when the server drains.
package catalogwire

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/admin/catalogscanapi"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Catalog is the assembled scan.
type Catalog struct {
	Settings *catalogscan.Store
	Loop     *catalogscan.Loop
	platform *platform.Platform
	admin    bool
}

// Platform is what the scan is assembled from.
type Platform interface {
	DB() *sql.DB
	ToolkitRegistry() *registry.Registry
}

// Build assembles the scan, or returns nil with no platform or no database.
func Build(p *platform.Platform) *Catalog {
	if p == nil || p.DB() == nil {
		return nil
	}
	c := assemble(p.DB(), p.ToolkitRegistry())
	c.platform, c.admin = p, p.Config().Admin.IsEnabled()
	return c
}

// assemble builds the scan over a database and the live toolkits.
func assemble(db *sql.DB, reg *registry.Registry) *Catalog {
	settings := catalogscan.NewStore(db)
	scanner := catalogscan.New(catalogstore.New(db), settings, settings,
		catalogscan.NewTrinoSource(trinoReader{reg: reg}),
		catalogscan.NewAPISource(apiReader{reg: reg}),
		catalogscan.NewGraphQLSource(graphqlReader{reg: reg}),
	)
	return &Catalog{Settings: settings, Loop: catalogscan.NewLoop(scanner, settings, db)}
}

// MountAdmin mounts the scan's settings and scan routes behind the admin API's
// authentication, when the admin API is enabled.
func (c *Catalog) MountAdmin(
	mux *http.ServeMux, auth func(*platform.Platform) func(http.Handler) http.Handler, author func(*http.Request) string,
) {
	if c == nil || !c.admin {
		return
	}
	catalogscanapi.Register(mux, auth(c.platform), catalogscanapi.Config{Store: c.Settings, Scanner: c.Loop, Author: author})
}

// Start begins scanning.
func (c *Catalog) Start(ctx context.Context) {
	if c != nil {
		c.Loop.Start(ctx)
	}
}

// Stop ends scanning.
func (c *Catalog) Stop() {
	if c != nil {
		c.Loop.Stop()
	}
}

// liveConnections lists the live connections of a kind with both names.
func liveConnections(reg *registry.Registry, kind string) []catalogscan.Connection {
	out := make([]catalogscan.Connection, 0)
	if reg == nil {
		return out
	}
	for _, c := range connid.NewResolver(reg.All(), nil).All(kind) {
		if c.Live {
			out = append(out, catalogscan.Connection{Instance: string(c.Instance), Bound: string(c.Bound)})
		}
	}
	return out
}

// trinoReader reads Trino through each trino toolkit's clients.
type trinoReader struct{ reg *registry.Registry }

// Connections are the live Trino connections.
func (r trinoReader) Connections() []catalogscan.Connection {
	return liveConnections(r.reg, catalogscan.KindTrino)
}

// client finds the client that serves a bound connection name.
func (r trinoReader) client(bound string) (*trinoclient.Client, error) {
	for _, tk := range r.reg.GetByKind(catalogscan.KindTrino) {
		t, ok := tk.(*trinokit.Toolkit)
		if !ok {
			continue
		}
		if m := t.Manager(); m != nil {
			if c, err := m.Client(bound); err == nil {
				return c, nil
			}
		}
	}
	return nil, fmt.Errorf("no trino client serves connection %q", bound)
}

// Catalogs lists the catalogs a Trino connection sees.
func (r trinoReader) Catalogs(ctx context.Context, bound string) ([]string, error) {
	c, err := r.client(bound)
	if err != nil {
		return nil, err
	}
	names, err := c.ListCatalogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing catalogs: %w", err)
	}
	return names, nil
}

// columnsQuery reads a catalog's columns with their table's type, a table's
// columns together and in order. information_schema describes the engine and
// is left out.
const columnsQuery = `SELECT c.table_schema, c.table_name, t.table_type, c.column_name, c.data_type, c.is_nullable
FROM %[1]s.information_schema.columns c
JOIN %[1]s.information_schema.tables t
  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE c.table_schema <> 'information_schema'
ORDER BY c.table_schema, c.table_name, c.ordinal_position`

// Columns reports every column of a catalog, a table's columns together and
// in order.
func (r trinoReader) Columns(ctx context.Context, bound, catalogName string, fn func(catalogscan.ColumnRow) error) error {
	c, err := r.client(bound)
	if err != nil {
		return err
	}
	// No row limit: a catalog is read whole, and a cap would be read as the
	// catalog's true extent.
	cur, err := c.QueryStream(ctx, fmt.Sprintf(columnsQuery, quoteIdent(catalogName)), trinoclient.QueryOptions{})
	if err != nil {
		return fmt.Errorf("reading columns: %w", err)
	}
	defer func() { _ = cur.Close() }()
	for cur.Next() {
		row := cur.Row()
		if err := fn(catalogscan.ColumnRow{
			Schema:    str(row["table_schema"]),
			Table:     str(row["table_name"]),
			TableType: str(row["table_type"]),
			Column:    str(row["column_name"]),
			DataType:  str(row["data_type"]),
			Nullable:  str(row["is_nullable"]) != "NO",
		}); err != nil {
			return err
		}
	}
	if err := cur.Err(); err != nil {
		return fmt.Errorf("reading columns: %w", err)
	}
	return nil
}

// quoteIdent quotes a Trino identifier.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// str reads a result value as text.
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// apiReader lists the stored OpenAPI operations of each API connection.
type apiReader struct{ reg *registry.Registry }

// Connections are the live API connections.
func (r apiReader) Connections() []catalogscan.Connection {
	return liveConnections(r.reg, apigateway.Kind)
}

// Operations lists an API connection's stored operations.
func (r apiReader) Operations(_ context.Context, bound string) ([]catalogscan.Operation, error) {
	for _, tk := range r.reg.GetByKind(apigateway.Kind) {
		t, ok := tk.(*apigateway.Toolkit)
		if !ok {
			continue
		}
		for _, c := range t.CatalogConnections() {
			if c.Name == bound {
				return apiOperations(c.Operations), nil
			}
		}
	}
	return nil, fmt.Errorf("no api connection %q", bound)
}

// apiOperations names each operation by its operation id. Two specs of one
// connection can use the same id; those are named by spec and id so neither
// replaces the other. An operation with no id has no stable name and is not
// cataloged.
func apiOperations(ops []apigateway.OperationSummary) []catalogscan.Operation {
	count := make(map[string]int, len(ops))
	for _, op := range ops {
		count[op.OperationID]++
	}
	out := make([]catalogscan.Operation, 0, len(ops))
	for _, op := range ops {
		if op.OperationID == "" {
			continue
		}
		path := []string{op.OperationID}
		if count[op.OperationID] > 1 && op.Spec != "" {
			path = []string{op.Spec, op.OperationID}
		}
		out = append(out, catalogscan.Operation{Path: path})
	}
	return out
}

// graphqlReader lists the operations of each GraphQL connection's schema.
type graphqlReader struct{ reg *registry.Registry }

// Connections are the live GraphQL connections.
func (r graphqlReader) Connections() []catalogscan.Connection {
	return liveConnections(r.reg, graphqlkit.Kind)
}

// Operations lists the operations of a GraphQL connection's stored schema.
func (r graphqlReader) Operations(_ context.Context, bound string) ([]catalogscan.Operation, error) {
	for _, tk := range r.reg.GetByKind(graphqlkit.Kind) {
		t, ok := tk.(*graphqlkit.Toolkit)
		if !ok {
			continue
		}
		ops, _, found := t.Operations(bound)
		if !found {
			continue
		}
		out := make([]catalogscan.Operation, 0, len(ops))
		for _, op := range ops {
			cols := make([]catalog.ColumnInput, 0, len(op.Arguments))
			for _, a := range op.Arguments {
				cols = append(cols, catalog.ColumnInput{Name: a.Name, DataType: a.Type, Nullable: !a.Required})
			}
			path := append([]string{strings.ToLower(string(op.Kind))}, op.Path...)
			out = append(out, catalogscan.Operation{Path: path, Columns: cols})
		}
		return out, nil
	}
	return nil, fmt.Errorf("no graphql connection %q, or its schema has not been read", bound)
}

// RegistrationStore wraps a registration store so each table it records is
// cataloged when it is recorded. With no database it returns inner.
func RegistrationStore(p Platform, inner tableregister.Store) tableregister.Store {
	if p == nil || p.DB() == nil {
		return inner
	}
	return tableregister.NewCatalogingStore(inner, catalogstore.New(p.DB()), trinoInstance(p.ToolkitRegistry()))
}

// trinoInstance maps the name a call binds a Trino connection by to its
// instance name, or returns the name as it stands for a connection nothing
// serves.
func trinoInstance(reg *registry.Registry) func(bound string) string {
	return func(bound string) string {
		if reg == nil {
			return bound
		}
		c, ok := connid.NewResolver(reg.All(), nil).ByBound(catalogscan.KindTrino, connid.Bound(bound))
		if !ok {
			return bound
		}
		return string(c.Instance)
	}
}
