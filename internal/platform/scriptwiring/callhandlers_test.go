package scriptwiring

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/sqlprotect"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// baseTk is a toolkit holding nothing.
type baseTk struct{ kind, name string }

func (b baseTk) Kind() string                        { return b.kind }
func (b baseTk) Name() string                        { return b.name }
func (b baseTk) Connection() string                  { return b.name }
func (baseTk) RegisterTools(*mcp.Server)             {}
func (baseTk) Tools() []string                       { return nil }
func (baseTk) SetSemanticProvider(semantic.Provider) {}
func (baseTk) SetQueryProvider(query.Provider)       {}
func (baseTk) Close() error                          { return nil }

// pgTk is an application database toolkit whose view v on orders is defined
// over people.
type pgTk struct {
	baseTk
	got []postgres.Statement
}

func (*pgTk) HasConnection(name string) bool { return name == "orders" }

func (p *pgTk) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	p.got = append(p.got, s)
	params, _ := s.Params.([]any)
	if len(params) == 2 && params[1] == "v" {
		return &postgres.Result{Rows: [][]any{{"SELECT ssn FROM app.people"}}}, nil
	}
	if len(params) == 2 && params[1] == "broken" {
		return nil, errors.New("down")
	}
	return &postgres.Result{}, nil
}

// trinoTk is a Trino toolkit whose view tv is defined over people.
type trinoTk struct {
	baseTk
	got []string
}

func (t *trinoTk) Query(_ context.Context, _, sql string, _ trinoclient.QueryOptions) (*trinoclient.QueryResult, error) {
	t.got = append(t.got, sql)
	if sql == viewQuery("hive", "app", "tv") {
		return &trinoclient.QueryResult{Rows: []map[string]any{{"view_definition": "SELECT ssn FROM hive.app.people"}}}, nil
	}
	return &trinoclient.QueryResult{}, nil
}

func toolkits(t *testing.T, tks ...registry.Toolkit) *registry.Registry {
	t.Helper()
	r := registry.NewRegistry()
	for _, tk := range tks {
		require.NoError(t, r.Register(tk))
	}
	return r
}

// TestViewsOn follows a view through PostgreSQL's catalog on an application
// database and through the information schema on Trino, so a protected
// column read through a view is refused on either.
func TestViewsOn(t *testing.T) {
	ctx := context.Background()
	pg := &pgTk{kind: "appdb", name: "orders"}
	tr := &trinoTk{kind: "trino", name: "acme"}
	reg := toolkits(t, pg, tr)

	views := viewsOn(reg, "orders")
	require.NotNil(t, views)
	assert.Equal(t, "SELECT ssn FROM app.people", views(ctx, "", "App", "V"), "an unquoted name is folded")
	assert.Equal(t, []any{"app", "v"}, pg.got[0].Params)
	assert.Equal(t, pgViewQuery, pg.got[0].SQL)
	assert.Empty(t, views(ctx, "", "", "people"), "a table is not a view")
	assert.Empty(t, views(ctx, "", "", "broken"), "a view that cannot be read is none")
	assert.Empty(t, tr.got, "an application database is not read through Trino")

	err := protectWith(reg)(ctx, "orders", "SELECT substr(ssn, 1, 3) FROM app.v", map[string]string{"app.people.ssn": "mask"})
	var refusal *sqlprotect.RefusalError
	require.ErrorAs(t, err, &refusal, "a protected column read through an application database view")

	trino := viewsOn(reg, "acme")
	require.NotNil(t, trino)
	assert.Equal(t, "SELECT ssn FROM hive.app.people", trino(ctx, "hive", "app", "tv"))
	err = protectWith(reg)(ctx, "acme", "SELECT substr(ssn, 1, 3) FROM hive.app.tv", map[string]string{"hive.app.people.ssn": "mask"})
	require.ErrorAs(t, err, &refusal, "a protected column read through a Trino view")

	assert.Nil(t, viewsOn(nil, "orders"))
	assert.Nil(t, viewsOn(toolkits(t, baseTk{kind: "s3", name: "files"}), "orders"), "no toolkit reads views")
}

// TestIdentifierAndViewQuery are the names a view is looked up by.
func TestIdentifierAndViewQuery(t *testing.T) {
	assert.Equal(t, "orders", identifier("Orders"))
	assert.Equal(t, "Orders", identifier(`"Orders"`))
	assert.Equal(t, `a"b`, identifier(`"a""b"`))
	assert.Equal(t, "SELECT view_definition FROM information_schema.views WHERE table_name = 'v'", viewQuery("", "", "V"))
	assert.Equal(t, `SELECT view_definition FROM "hi""ve".information_schema.views WHERE table_name = 'o''v' AND table_schema = 'app'`,
		viewQuery(`hi"ve`, "App", "o'v"))
}

// TestShapesFrom finds the toolkit of a kind that answers an api call's
// operation.
func TestShapesFrom(t *testing.T) {
	assert.Nil(t, shapesFrom(toolkits(t, baseTk{kind: "api", name: "billing"}))("api"), "a toolkit that answers no operation")
	assert.NotNil(t, shapesFrom(toolkits(t, apiTk{kind: "api", name: "billing"}))("api"))
	assert.Nil(t, shapesFrom(nil)("api"))
}

// apiTk is an api toolkit answering operations.
type apiTk struct{ baseTk }

func (apiTk) OperationKey(context.Context, string, string, string, string) string { return "op" }
func (apiTk) ResolveOperationRequest(context.Context, string, string, string, map[string]string) (method, path string, ok bool) {
	return "GET", "/", true
}
func (apiTk) OperationTemplate(context.Context, string, string, string) string { return "" }
