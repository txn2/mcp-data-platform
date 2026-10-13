package catalogwire

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/indexpolicy"
	"github.com/txn2/mcp-data-platform/internal/platform/catalogscan"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeDocWriter records the creates and revisions the keeper asks for.
type fakeDocWriter struct {
	created []resource.NewResource
	content []byte
	revised []string
	err     error
}

func (f *fakeDocWriter) Create(_ context.Context, in resource.NewResource, claims resource.Claims) (*resource.Resource, error) {
	if f.err != nil {
		return nil, f.err
	}
	if claims.Sub != catalogPrincipal || !claims.IsAdmin {
		return nil, errors.New("not the catalog's authority")
	}
	f.content, _ = io.ReadAll(in.Content)
	f.created = append(f.created, in)
	return &resource.Resource{ID: "new-id"}, nil
}

func (f *fakeDocWriter) ReplaceIfChanged(_ context.Context, id string, content []byte, _ string, _ resource.Claims) (*resource.Resource, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	f.revised = append(f.revised, id)
	f.content = content
	return &resource.Resource{ID: id}, true, nil
}

// fakeURIs answers lookups from a map of address to resource.
type fakeURIs struct {
	byURI map[string]*resource.Resource
	err   error
}

func (f fakeURIs) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	if f.err != nil {
		return nil, f.err
	}
	if r, ok := f.byURI[uri]; ok {
		return r, nil
	}
	return nil, sql.ErrNoRows
}

var specDoc = catalogscan.Document{Key: "orders", Filename: "orders.yaml", MIMEType: "application/yaml", Content: []byte("openapi: 3.0.0")}

// A document is created in the global library under catalog/<kind>/<connection>
// the first time, as the catalog scan's write.
func TestDocumentKeeperCreates(t *testing.T) {
	w := &fakeDocWriter{}
	k := documentKeeper{w: w, byURI: fakeURIs{byURI: map[string]*resource.Resource{}}, uriScheme: "mcp"}
	id, err := k.Keep(context.Background(), "api", "billing", specDoc)
	require.NoError(t, err)
	assert.Equal(t, "new-id", id)
	require.Len(t, w.created, 1)
	in := w.created[0]
	assert.Equal(t, resource.ScopeGlobal, in.Scope)
	assert.Equal(t, "catalog/api/billing", in.Path)
	assert.Equal(t, "orders.yaml", in.Filename)
	assert.Equal(t, indexpolicy.OriginCatalog, in.Origin)
	assert.Equal(t, "openapi: 3.0.0", string(w.content))
}

// A document already kept is offered to the resource as its next version.
func TestDocumentKeeperRevisesTheKeptResource(t *testing.T) {
	uri := resource.BuildURI("mcp", resource.ScopeGlobal, "", "catalog/api/billing", "orders.yaml")
	w := &fakeDocWriter{}
	k := documentKeeper{w: w, byURI: fakeURIs{byURI: map[string]*resource.Resource{uri: {ID: "kept"}}}, uriScheme: "mcp"}
	id, err := k.Keep(context.Background(), "api", "billing", specDoc)
	require.NoError(t, err)
	assert.Equal(t, "kept", id)
	assert.Equal(t, []string{"kept"}, w.revised)
	assert.Empty(t, w.created)
}

func TestDocumentKeeperErrors(t *testing.T) {
	boom := errors.New("boom")
	k := documentKeeper{w: &fakeDocWriter{}, byURI: fakeURIs{err: boom}, uriScheme: "mcp"}
	_, err := k.Keep(context.Background(), "api", "billing", specDoc)
	assert.ErrorIs(t, err, boom)

	k = documentKeeper{w: &fakeDocWriter{err: boom}, byURI: fakeURIs{byURI: map[string]*resource.Resource{}}, uriScheme: "mcp"}
	_, err = k.Keep(context.Background(), "api", "billing", specDoc)
	assert.ErrorIs(t, err, boom)

	uri := resource.BuildURI("mcp", resource.ScopeGlobal, "", "catalog/api/billing", "orders.yaml")
	k = documentKeeper{w: &fakeDocWriter{err: boom}, byURI: fakeURIs{byURI: map[string]*resource.Resource{uri: {ID: "kept"}}}, uriScheme: "mcp"}
	_, err = k.Keep(context.Background(), "api", "billing", specDoc)
	assert.ErrorIs(t, err, boom)
}

// The import runner is assembled over the database, and a DataHub client is
// opened for the address an administrator names.
func TestImportAssembly(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	assert.NotNil(t, importRunner(db, platform.URNMappingConfig{Platform: "postgres"}))

	src, err := openDataHub("http://datahub.example.com:8080", "token")
	require.NoError(t, err)
	assert.NotNil(t, src)
	_, err = openDataHub("", "token")
	assert.Error(t, err, "a client with no address cannot be opened")
}
