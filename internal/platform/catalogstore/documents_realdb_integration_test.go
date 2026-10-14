//go:build integration

package catalogstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// A schema document records a version only when its content or media type
// changes, and a dataset names the document it was read from under its own
// connection (#1918).
func TestStore_SchemaDocuments_RealDB(t *testing.T) {
	ctx := context.Background()
	s := New(testdb.New(t))
	in := catalog.DocumentInput{
		ConnectionKind: "api", Connection: "billing", Filename: "openapi.json",
		MIMEType: "application/json", Content: []byte(`{"openapi":"3.0.0"}`),
	}

	first, changed, err := s.PutDocument(ctx, in)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, 1, first.Version)

	same, changed, err := s.PutDocument(ctx, in)
	require.NoError(t, err)
	assert.False(t, changed, "the same content records no version")
	assert.Equal(t, 1, same.Version)
	assert.Equal(t, first.ID, same.ID)
	assert.Equal(t, len(in.Content), same.SizeBytes)

	in.Content = []byte(`{"openapi":"3.1.0"}`)
	next, changed, err := s.PutDocument(ctx, in)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, 2, next.Version)

	in.MIMEType = "application/vnd.oai.openapi+json"
	retyped, changed, err := s.PutDocument(ctx, in)
	require.NoError(t, err)
	assert.True(t, changed, "a new media type is a new version")
	assert.Equal(t, 3, retyped.Version)

	ref := catalog.SchemaDocumentRef("api", "billing", "openapi.json")
	got, err := s.GetDocument(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, `{"openapi":"3.1.0"}`, string(got.Content))
	assert.Equal(t, "application/vnd.oai.openapi+json", got.MIMEType)
	_, err = s.GetDocument(ctx, catalog.SchemaDocumentRef("api", "other", "openapi.json"))
	require.ErrorIs(t, err, catalog.ErrNotFound, "a document belongs to one connection")

	_, _, err = s.PutDocument(ctx, catalog.DocumentInput{
		ConnectionKind: "graphql", Connection: "crm", Filename: "schema.graphql",
		MIMEType: "application/graphql", Content: []byte("type Query { a: Int }"),
	})
	require.NoError(t, err)
	docs, err := s.ListDocuments(ctx)
	require.NoError(t, err)
	require.Len(t, docs, 2)
	assert.Equal(t, ref.String(), docs[0].Reference)
	assert.Equal(t, 3, docs[0].Version)
	assert.Equal(t, 19, docs[0].SizeBytes)
	assert.Nil(t, docs[0].Content)

	op := catalog.DatasetRef("api", "billing", "listInvoices")
	d, err := s.PutDataset(ctx, catalog.DatasetInput{Ref: op, Type: catalog.DatasetAPIOperation, SchemaDocument: "openapi.json"})
	require.NoError(t, err)
	assert.Equal(t, ref.String(), d.SchemaDocument)
	d, err = s.PutDataset(ctx, catalog.DatasetInput{Ref: op, Type: catalog.DatasetAPIOperation})
	require.NoError(t, err)
	assert.Equal(t, ref.String(), d.SchemaDocument, "a scan that kept no document leaves the link it had")
}
