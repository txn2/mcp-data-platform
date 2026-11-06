package applicationwire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/apphttp"
	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// TestDatabaseChoices is #2491's choices: only the appdb connections the
// caller's persona reaches, each with its schemas, a schema an application
// holds marked with that application (and listed before its first release
// creates it), and a connection whose schemas cannot be read still offered
// with a problem that repeats nothing the database said.
func TestDatabaseChoices(t *testing.T) {
	var reachedAs struct {
		persona string
		admin   bool
	}
	src := databaseSources{
		reach: func(_ context.Context, persona string, admin bool) []connreach.Connection {
			reachedAs.persona, reachedAs.admin = persona, admin
			return []connreach.Connection{
				{Name: "orders", Kind: postgreskit.Kind, Description: "Orders database"},
				{Name: "warehouse", Kind: "trino"},
				{Name: "broken", Kind: postgreskit.Kind},
			}
		},
		persona: func(roles []string) string { return roles[0] + "-persona" },
		schemas: func(_ context.Context, c string) ([]string, error) {
			if c == "broken" {
				return nil, errors.New("dial tcp postgres://u:hunter2@db.internal:5432 refused")
			}
			return []string{"public", "shop"}, nil
		},
		held: func(context.Context) ([]application.Application, error) {
			return []application.Application{
				{Name: "shop", Connection: "orders", Schema: "shop"},
				{Name: "billing", Connection: "orders", Schema: "billing"},
				{Name: "notes"},
			}, nil
		},
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	got := src.choices(r, application.Actor{Roles: []string{"analyst"}})
	assert.Equal(t, "analyst-persona", reachedAs.persona)
	assert.False(t, reachedAs.admin)
	require.Len(t, got, 2, "the trino connection is not offered")
	assert.Equal(t, apphttp.DatabaseChoice{Connection: "orders", Description: "Orders database", Schemas: []apphttp.SchemaChoice{
		{Name: "billing", HeldBy: "billing"}, {Name: "public"}, {Name: "shop", HeldBy: "shop"},
	}}, got[0])
	assert.Equal(t, "broken", got[1].Connection)
	assert.NotEmpty(t, got[1].Problem)
	assert.NotContains(t, got[1].Problem, "hunter2")
	assert.NotContains(t, got[1].Problem, "db.internal")
	assert.Empty(t, got[1].Schemas)

	src.held = func(context.Context) ([]application.Application, error) { return nil, errors.New("down") }
	got = src.choices(r, application.Actor{Roles: []string{"x"}, Admin: true})
	assert.True(t, reachedAs.admin)
	assert.Equal(t, []apphttp.SchemaChoice{{Name: "public"}, {Name: "shop"}}, got[0].Schemas)

	assert.Nil(t, databaseSources{}.choices(r, application.Actor{}))
}

func TestReadSchemasWithoutAToolkit(t *testing.T) {
	_, err := readSchemas(func() *postgreskit.Toolkit { return nil })(context.Background(), "orders")
	require.ErrorIs(t, err, errNoKit)
}
