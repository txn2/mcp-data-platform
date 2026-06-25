package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePageProvider struct {
	pages   []KnowledgePageSnippet
	err     error
	gotURNs []string
}

func (f *fakePageProvider) PagesForEntities(_ context.Context, urns []string, _ int) ([]KnowledgePageSnippet, error) {
	f.gotURNs = urns
	return f.pages, f.err
}

func resultWithURN() *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
		Text: `{"urn":"urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"}`,
	}}}
}

func TestEnrichWithKnowledgePages_AppendsBlock(t *testing.T) {
	kp := &fakePageProvider{pages: []KnowledgePageSnippet{{ID: "kp1", Slug: "vocab", Title: "ACME Vocabulary"}}}
	urn := "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"
	res := enrichWithKnowledgePages(context.Background(), kp, resultWithURN(), []string{urn})

	require.Len(t, res.Content, 2, "a knowledge_pages block is appended")
	tc, ok := res.Content[1].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "knowledge_pages")
	assert.Contains(t, tc.Text, "ACME Vocabulary")
	assert.Equal(t, []string{urn}, kp.gotURNs)
}

func TestEnrichWithKnowledgePages_NoopPaths(t *testing.T) {
	urn := "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"

	// Nil provider leaves the result untouched.
	res := resultWithURN()
	assert.Same(t, res, enrichWithKnowledgePages(context.Background(), nil, res, []string{urn}))
	assert.Len(t, res.Content, 1)

	// No entity URNs: no enrichment.
	noURN := resultWithURN()
	out := enrichWithKnowledgePages(context.Background(), &fakePageProvider{pages: []KnowledgePageSnippet{{ID: "kp1"}}}, noURN, nil)
	assert.Len(t, out.Content, 1, "no entity urns -> no block")

	// Provider error: result unchanged.
	out = enrichWithKnowledgePages(context.Background(), &fakePageProvider{err: errors.New("boom")}, resultWithURN(), []string{urn})
	assert.Len(t, out.Content, 1, "provider error -> result unchanged")

	// No referencing pages: no block.
	out = enrichWithKnowledgePages(context.Background(), &fakePageProvider{}, resultWithURN(), []string{urn})
	assert.Len(t, out.Content, 1, "no pages -> no block")
}

func TestEnrichWithKnowledgePages_CapsURNFanout(t *testing.T) {
	kp := &fakePageProvider{}
	urns := make([]string, maxKnowledgeEnrichmentURNs+5)
	for i := range urns {
		urns[i] = "urn:li:dataset:(urn:li:dataPlatform:trino,a.b." + string(rune('a'+i)) + ",PROD)"
	}
	enrichWithKnowledgePages(context.Background(), kp, resultWithURN(), urns)
	assert.Len(t, kp.gotURNs, maxKnowledgeEnrichmentURNs, "the looked-up urn set is capped")
}
