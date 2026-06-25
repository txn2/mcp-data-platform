package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

type fakeReverseLookup struct {
	pages []knowledgepage.PageRef
}

func (f fakeReverseLookup) ListPagesReferencing(_ context.Context, _ knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	return f.pages, nil
}

func TestKnowledgePageEnrichmentBridge_PagesForEntities(t *testing.T) {
	b := &knowledgePageEnrichmentBridge{store: fakeReverseLookup{pages: []knowledgepage.PageRef{
		{ID: "kp1", Slug: "vocab", Title: "ACME Vocabulary"},
	}}}
	got, err := b.PagesForEntities(context.Background(),
		[]string{"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}, 5)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, middleware.KnowledgePageSnippet{ID: "kp1", Slug: "vocab", Title: "ACME Vocabulary"}, got[0])
}

func TestKnowledgePageProviders(t *testing.T) {
	assert.Nil(t, knowledgePageProviders(nil), "no store -> empty variadic")
	got := knowledgePageProviders(fakeReverseLookup{})
	require.Len(t, got, 1, "a store yields one provider")
}
