//go:build integration

package graphqlstore

// The real-database proof for #1676 and #1703: a stored schema survives a
// configuration save whose re-read fails, a second toolkit instance over the
// same database serves it, and a re-read the endpoint refuses is reported
// beside that schema by an instance that did not run the read and by one
// started afterwards. sqlmock returns whatever rows a test hands
// it, so only a real PostgreSQL shows that the row an upload wrote is the
// row a save reads back, that a deletion is what removes it, and that the
// row one instance wrote is what another instance installs.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// refusingEndpoint answers the introspection query the way the ticket's
// endpoint did, HTTP 302 with no GraphQL body, and every other document
// with an empty data object.
func refusingEndpoint(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "__schema") {
			w.Header().Set("Location", "/login")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// uploadedSchema is what an operator supplies for an endpoint that will not
// answer: two operations, so a surviving count is a whole schema.
const uploadedSchema = `
schema { query: Query }
type Query {
  "Read one thing."
  thing(id: ID!): Thing
  "Read every thing."
  things: [Thing!]!
}
type Thing { id: ID!, label: String }
`

// instance builds one toolkit over the store, holding one connection to the
// endpoint; the registration's read fails and is recorded.
func instance(t *testing.T, store *Store, endpoint string) *graphqlkit.Toolkit {
	t.Helper()
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{})
	tk.SetSchemaStore(store)
	tk.SetVectorReader(store)
	require.NoError(t, tk.AddConnection("erp", map[string]any{"endpoint_url": endpoint, "connect_timeout": "5s"}))
	return tk
}

func TestRealDB_StoredSchemaSurvivesASaveWhoseRereadFails(t *testing.T) {
	db := testdb.New(t)
	store := New(db)
	ctx := context.Background()
	endpoint := refusingEndpoint(t)
	tk := instance(t, store, endpoint)

	before, err := tk.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, 0, before.OperationCount)
	assert.Contains(t, before.Error, "HTTP 302", "the registration's read is refused and recorded")

	require.NoError(t, tk.SetSchema(ctx, "erp", []byte(uploadedSchema)))
	uploaded, err := tk.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, graphqlkit.SchemaSourceUpload, uploaded.Source)
	assert.Equal(t, 2, uploaded.OperationCount)
	assert.Empty(t, uploaded.Error)

	// One key changed: the save the ticket was reported from.
	require.NoError(t, tk.UpdateConnection("erp", map[string]any{
		"endpoint_url": endpoint, "connect_timeout": "5s", "read_only": true,
	}))

	after, err := tk.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, uploaded.Hash, after.Hash, "the save kept the uploaded version")
	assert.Equal(t, graphqlkit.SchemaSourceUpload, after.Source)
	assert.Equal(t, 2, after.OperationCount)
	assert.Contains(t, after.Error, "HTTP 302", "the failed re-read is reported beside the schema")

	stored, err := store.GetSchema(ctx, "erp")
	require.NoError(t, err)
	assert.Equal(t, uploaded.Hash, stored.Hash, "the row the upload wrote is the row the save read back")
	assert.Equal(t, graphqlkit.SchemaSourceUpload, stored.Source)
}

func TestRealDB_ASecondInstanceServesTheStoredSchema(t *testing.T) {
	db := testdb.New(t)
	store := New(db)
	ctx := context.Background()
	endpoint := refusingEndpoint(t)
	first := instance(t, store, endpoint)
	require.NoError(t, first.SetSchema(ctx, "erp", []byte(uploadedSchema)))
	onFirst, err := first.SchemaInfo("erp")
	require.NoError(t, err)

	// The replica that did not serve the upload, registering the connection
	// from the reload bus: its own read fails and the store is what it
	// serves. That read was about no schema, not about the upload, so it
	// answers what the first instance answers (#1703).
	second := instance(t, store, endpoint)
	onSecond, err := second.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, onFirst.Hash, onSecond.Hash, "both instances hold one schema for one connection")
	assert.Equal(t, graphqlkit.SchemaSourceUpload, onSecond.Source)
	assert.Equal(t, 2, onSecond.OperationCount)
	assert.Equal(t, onFirst.Error, onSecond.Error, "both instances report one state")

	// A peer's announcement installs the store's version with the store's
	// refusal beside it, the same way on an instance that already held the
	// schema.
	require.NoError(t, second.LoadStoredSchema(ctx, "erp"))
	announced, err := second.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, onFirst.Hash, announced.Hash)
	assert.Empty(t, announced.Error)
	assert.True(t, announced.FetchedAt.Equal(onFirst.FetchedAt), "fetched_at is the store's, not the instance's")

	// A deletion is what drops the row: the second instance's removal takes
	// it, and the first instance then finds nothing to load.
	require.NoError(t, second.RemoveConnection("erp"))
	_, err = store.GetSchema(ctx, "erp")
	require.ErrorIs(t, err, graphqlkit.ErrSchemaNotFound)
	require.ErrorIs(t, first.LoadStoredSchema(ctx, "erp"), graphqlkit.ErrSchemaNotFound)
}

// TestRealDB_ARefusalRecordedThroughOneInstanceIsReadThroughASecond is
// #1703's first defect: a re-read the endpoint refused was held in the memory
// of the instance that ran it, so another instance over the same database
// answered the same connection with no error.
func TestRealDB_ARefusalRecordedThroughOneInstanceIsReadThroughASecond(t *testing.T) {
	db := testdb.New(t)
	store := New(db)
	ctx := context.Background()
	endpoint := refusingEndpoint(t)
	first := instance(t, store, endpoint)
	second := instance(t, store, endpoint)

	// An upload clears whatever the registrations recorded, on the row and,
	// through the announcement, on the instance that did not serve it.
	require.NoError(t, first.SetSchema(ctx, "erp", []byte(uploadedSchema)))
	require.NoError(t, second.LoadStoredSchema(ctx, "erp"))
	clean, err := second.SchemaInfo("erp")
	require.NoError(t, err)
	require.Empty(t, clean.Error, "the upload is the answer, with nothing beside it")

	// The re-read runs on the first instance only.
	require.Error(t, first.RefreshSchema(ctx, "erp"))
	require.NoError(t, second.LoadStoredSchema(ctx, "erp"))
	onSecond, err := second.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, clean.Hash, onSecond.Hash, "the refusal left the uploaded schema in place")
	assert.Equal(t, graphqlkit.SchemaSourceUpload, onSecond.Source)
	assert.Contains(t, onSecond.Error, "HTTP 302", "the instance that did not run the read reports its refusal")

	stored, err := store.GetSchema(ctx, "erp")
	require.NoError(t, err)
	assert.Equal(t, clean.Hash, stored.Hash, "recording the refusal did not touch the schema")
	assert.Contains(t, stored.ReadError, "HTTP 302")

	// An upload after the refusal clears it on the row.
	require.NoError(t, first.SetSchema(ctx, "erp", []byte(uploadedSchema)))
	cleared, err := store.GetSchema(ctx, "erp")
	require.NoError(t, err)
	assert.Empty(t, cleared.ReadError, "a schema that was installed has no refusal beside it")
}

// TestRealDB_ARefusalSurvivesARestart is #1703's second defect: a restart kept
// the schema, its hash and fetched_at, and forgot the refusal. The restarted
// instance is built the way the platform boots one: the connection registers
// before the store is wired, then the schemas are hydrated from the store.
// Its endpoint is unreachable rather than refusing, so the 302 it reports can
// only have come from the row.
func TestRealDB_ARefusalSurvivesARestart(t *testing.T) {
	db := testdb.New(t)
	store := New(db)
	ctx := context.Background()
	before := instance(t, store, refusingEndpoint(t))
	require.NoError(t, before.SetSchema(ctx, "erp", []byte(uploadedSchema)))
	require.Error(t, before.RefreshSchema(ctx, "erp"))
	held, err := before.SchemaInfo("erp")
	require.NoError(t, err)

	unreachable := httptest.NewServer(http.NotFoundHandler())
	unreachableURL := unreachable.URL
	unreachable.Close()

	restarted := graphqlkit.NewMulti(graphqlkit.MultiConfig{})
	require.NoError(t, restarted.AddConnection("erp", map[string]any{"endpoint_url": unreachableURL, "connect_timeout": "2s"}))
	restarted.SetSchemaStore(store)
	restarted.SetVectorReader(store)
	restarted.HydrateSchemas(ctx)

	after, err := restarted.SchemaInfo("erp")
	require.NoError(t, err)
	assert.Equal(t, held.Hash, after.Hash)
	assert.True(t, after.FetchedAt.Equal(held.FetchedAt))
	assert.Contains(t, after.Error, "HTTP 302", "the refusal the store records is reported after the restart")
}
