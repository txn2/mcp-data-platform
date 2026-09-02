package searchfed

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// The dataset the catalog holds, and the fact an agent applied to its
// description. The query in the test below shares no word with the dataset's
// name or its table identifier, which is the condition #1131 is about.
const (
	refundURN = "urn:li:dataset:(urn:li:dataPlatform:trino,finance.ledger.gl_entries,PROD)"
	refundDoc = "Refunds are subtracted before revenue is recognized."
)

// keywordBlindCatalog is a semantic.Provider whose keyword search finds nothing
// (the condition this feature exists for: DataHub matches words, so a topical
// query misses a description phrased differently) but which still resolves the
// dataset by identity, which is what fetch dereferences against.
type keywordBlindCatalog struct {
	semantic.Provider
	searched bool
}

func (c *keywordBlindCatalog) SearchTables(context.Context, semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	c.searched = true
	return nil, nil
}

// errNoSuchTable is how DataHub reports an entity it does not hold: an error,
// not an empty result (see CatalogProvider.Fetch).
var errNoSuchTable = errors.New("no such dataset")

func (*keywordBlindCatalog) GetTableContext(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	if table.String() != "finance.ledger.gl_entries" {
		return nil, errNoSuchTable
	}
	return &semantic.TableContext{URN: refundURN, Description: refundDoc}, nil
}

// fixedCatalogIndex is the platform's own catalog index, standing in for the
// Postgres-backed one (whose SQL the real-DB gate covers).
type fixedCatalogIndex struct {
	hits []knowledge.CatalogIndexHit
	got  knowledge.CatalogIndexQuery
}

func (f *fixedCatalogIndex) SearchCatalogIndex(_ context.Context, q knowledge.CatalogIndexQuery) ([]knowledge.CatalogIndexHit, error) {
	f.got = q
	return f.hits, nil
}

// TestCatalogIndexReachableThroughAssembledSearch is the acceptance criterion
// for #1131, asserted through the read path the platform actually assembles
// rather than against the provider in isolation: a DataHub-resident dataset
// description is returned by `search` for a topical query that names none of
// its entities, and the hit carries the catalog reference that `fetch`
// dereferences.
func TestCatalogIndexReachableThroughAssembledSearch(t *testing.T) {
	catalog := &keywordBlindCatalog{}
	index := &fixedCatalogIndex{hits: []knowledge.CatalogIndexHit{{
		URN:         refundURN,
		Name:        "finance.ledger.gl_entries",
		Description: refundDoc,
		Score:       0.93,
	}}}

	h := New(Config{
		ToolkitName:      "default",
		CatalogEnabled:   true,
		SemanticProvider: catalog,
		CatalogIndex:     index,
		Registry:         registry.NewRegistry(),
	})
	require.NotNil(t, h)
	router := h.Router()
	require.NotNil(t, router)

	ctx := context.Background()
	res, err := router.Search(ctx, knowledge.Query{Intent: "how do we treat customer refunds", Limit: 5})
	require.NoError(t, err)

	var hit *knowledge.Hit
	for i := range res.Groups {
		if res.Groups[i].Source != knowledge.SourceCatalog {
			continue
		}
		for j := range res.Groups[i].Hits {
			if res.Groups[i].Hits[j].Ref == refundURN {
				hit = &res.Groups[i].Hits[j]
			}
		}
	}
	require.NotNil(t, hit, "the catalog description must be reachable from a query naming no entity")
	assert.Contains(t, hit.Text, refundDoc, "the hit carries the applied fact, not just the dataset name")
	assert.Equal(t, refundURN, hit.Reference, "the hit carries its catalog reference")
	assert.True(t, catalog.searched, "DataHub's own search still runs as the recall tail")
	assert.Equal(t, "how do we treat customer refunds", index.got.QueryText)

	// The other half of the criterion: that reference dereferences through the
	// same assembled router.
	doc, err := router.Fetch(ctx, hit.Reference, knowledge.Caller{})
	require.NoError(t, err)
	require.NotNil(t, doc)
	assert.Equal(t, knowledge.SourceCatalog, doc.Source)
	assert.Equal(t, refundURN, doc.Reference)
	ds, ok := doc.Content.(knowledge.CatalogDataset)
	require.True(t, ok, "a catalog fetch returns the dataset's record")
	assert.Equal(t, refundDoc, ds.Description)
}

// TestCatalogIndexAbsentLeavesCatalogUnchanged proves the index is additive: a
// deployment without one (no database, or the operator opted out) still gets
// the DataHub-ranked catalog source it had before.
func TestCatalogIndexAbsentLeavesCatalogUnchanged(t *testing.T) {
	catalog := &keywordBlindCatalog{}
	h := New(Config{
		ToolkitName:      "default",
		CatalogEnabled:   true,
		SemanticProvider: catalog,
		CatalogIndex:     nil,
		Registry:         registry.NewRegistry(),
	})
	require.NotNil(t, h)
	assert.Contains(t, providerNames(t, h), knowledge.SourceCatalog)

	_, err := h.Router().Search(context.Background(), knowledge.Query{Intent: "refunds"})
	require.NoError(t, err)
	assert.True(t, catalog.searched)
}
