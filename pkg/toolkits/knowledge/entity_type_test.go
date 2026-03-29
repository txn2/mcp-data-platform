package knowledge

import (
	"errors"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntityTypeFromURN(t *testing.T) {
	tests := []struct {
		name       string
		urn        string
		wantType   string
		wantErr    bool
		errContain string
	}{
		{
			name:     "dataset",
			urn:      "urn:li:dataset:(urn:li:dataPlatform:trino,test.table,PROD)",
			wantType: "dataset",
		},
		{
			name:     "domain",
			urn:      "urn:li:domain:sales",
			wantType: "domain",
		},
		{
			name:     "glossaryTerm",
			urn:      "urn:li:glossaryTerm:Revenue",
			wantType: "glossaryTerm",
		},
		{
			name:     "dataProduct",
			urn:      "urn:li:dataProduct:analytics",
			wantType: "dataProduct",
		},
		{
			name:     "dashboard",
			urn:      "urn:li:dashboard:(looker,abc123)",
			wantType: "dashboard",
		},
		{
			name:     "tag",
			urn:      "urn:li:tag:pii",
			wantType: "tag",
		},
		{
			name:       "invalid URN",
			urn:        "not-a-urn",
			wantErr:    true,
			errContain: "invalid URN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := entityTypeFromURN(tc.urn)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContain)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantType, got)
		})
	}
}

func TestValidateEntityTypeForChange(t *testing.T) {
	datasetURN := "urn:li:dataset:(urn:li:dataPlatform:trino,test.table,PROD)"
	domainURN := "urn:li:domain:sales"
	glossaryURN := "urn:li:glossaryTerm:Revenue"
	dataProductURN := "urn:li:dataProduct:analytics"
	tagURN := "urn:li:tag:pii"

	tests := []struct {
		name       string
		urn        string
		change     ApplyChange
		wantErr    bool
		errContain string
	}{
		// update_description on various entity types
		{
			name:   "update_description on dataset",
			urn:    datasetURN,
			change: ApplyChange{ChangeType: "update_description", Detail: "desc"},
		},
		{
			name:   "update_description on domain",
			urn:    domainURN,
			change: ApplyChange{ChangeType: "update_description", Detail: "desc"},
		},
		{
			name:   "update_description on glossaryTerm",
			urn:    glossaryURN,
			change: ApplyChange{ChangeType: "update_description", Detail: "desc"},
		},
		{
			name:   "update_description on dataProduct",
			urn:    dataProductURN,
			change: ApplyChange{ChangeType: "update_description", Detail: "desc"},
		},

		// update_description on unsupported entity types
		{
			name:       "update_description on tag (unsupported)",
			urn:        tagURN,
			change:     ApplyChange{ChangeType: "update_description", Detail: "desc"},
			wantErr:    true,
			errContain: "update_description is not supported for tag entities",
		},

		// Column-level descriptions: dataset only
		{
			name:   "column description on dataset",
			urn:    datasetURN,
			change: ApplyChange{ChangeType: "update_description", Target: "column:customer_id", Detail: "desc"},
		},
		{
			name:       "column description on domain",
			urn:        domainURN,
			change:     ApplyChange{ChangeType: "update_description", Target: "column:name", Detail: "desc"},
			wantErr:    true,
			errContain: "column-level update_description is only supported for datasets",
		},
		{
			name:       "column description on glossaryTerm",
			urn:        glossaryURN,
			change:     ApplyChange{ChangeType: "update_description", Target: "column:name", Detail: "desc"},
			wantErr:    true,
			errContain: "column-level update_description is only supported for datasets",
		},

		// add_curated_query: dataset only
		{
			name:   "curated query on dataset",
			urn:    datasetURN,
			change: ApplyChange{ChangeType: "add_curated_query", Detail: "My Query", QuerySQL: "SELECT 1"},
		},
		{
			name:       "curated query on domain",
			urn:        domainURN,
			change:     ApplyChange{ChangeType: "add_curated_query", Detail: "My Query", QuerySQL: "SELECT 1"},
			wantErr:    true,
			errContain: "add_curated_query is only supported for datasets",
		},
		{
			name:       "curated query on glossaryTerm",
			urn:        glossaryURN,
			change:     ApplyChange{ChangeType: "add_curated_query", Detail: "My Query", QuerySQL: "SELECT 1"},
			wantErr:    true,
			errContain: "add_curated_query is only supported for datasets",
		},

		// Operations that work on all entity types
		{
			name:   "add_tag on domain",
			urn:    domainURN,
			change: ApplyChange{ChangeType: "add_tag", Detail: "important"},
		},
		{
			name:   "remove_tag on glossaryTerm",
			urn:    glossaryURN,
			change: ApplyChange{ChangeType: "remove_tag", Detail: "deprecated"},
		},
		{
			name:   "add_glossary_term on dataProduct",
			urn:    dataProductURN,
			change: ApplyChange{ChangeType: "add_glossary_term", Detail: "revenue"},
		},
		{
			name:   "add_documentation on domain",
			urn:    domainURN,
			change: ApplyChange{ChangeType: "add_documentation", Target: "https://docs.example.com", Detail: "docs"},
		},
		{
			name:   "flag_quality_issue on domain",
			urn:    domainURN,
			change: ApplyChange{ChangeType: "flag_quality_issue", Detail: "stale data"},
		},

		// Error messages include supported operations
		{
			name:       "curated query error lists supported ops",
			urn:        domainURN,
			change:     ApplyChange{ChangeType: "add_curated_query", Detail: "q", QuerySQL: "SELECT 1"},
			wantErr:    true,
			errContain: "Supported operations for domain",
		},
		{
			name:       "column desc error lists supported ops for tag entity",
			urn:        tagURN,
			change:     ApplyChange{ChangeType: "update_description", Target: "column:name", Detail: "desc"},
			wantErr:    true,
			errContain: "Supported operations for tag",
		},

		// Context document operations: supported entity types
		{
			name:   "add_context_document on dataset",
			urn:    datasetURN,
			change: ApplyChange{ChangeType: "add_context_document", Target: "Title", Detail: "content"},
		},
		{
			name:   "add_context_document on glossaryTerm",
			urn:    glossaryURN,
			change: ApplyChange{ChangeType: "add_context_document", Target: "Title", Detail: "content"},
		},
		{
			name:   "add_context_document on container",
			urn:    "urn:li:container:abc",
			change: ApplyChange{ChangeType: "add_context_document", Target: "Title", Detail: "content"},
		},
		{
			name:   "update_context_document on dataset",
			urn:    datasetURN,
			change: ApplyChange{ChangeType: "update_context_document", Target: "doc-123", Detail: "new content"},
		},
		{
			name:   "remove_context_document on dataset",
			urn:    datasetURN,
			change: ApplyChange{ChangeType: "remove_context_document", Target: "doc-123"},
		},

		// Context document operations: unsupported entity types
		{
			name:       "add_context_document on domain (unsupported)",
			urn:        domainURN,
			change:     ApplyChange{ChangeType: "add_context_document", Target: "Title", Detail: "content"},
			wantErr:    true,
			errContain: "add_context_document is only supported for datasets, glossaryTerms, glossaryNodes, and containers",
		},
		{
			name:       "update_context_document on tag (unsupported)",
			urn:        tagURN,
			change:     ApplyChange{ChangeType: "update_context_document", Target: "doc-123", Detail: "content"},
			wantErr:    true,
			errContain: "update_context_document is only supported for datasets, glossaryTerms, glossaryNodes, and containers",
		},
		{
			name:       "remove_context_document on domain (unsupported)",
			urn:        domainURN,
			change:     ApplyChange{ChangeType: "remove_context_document", Target: "doc-123"},
			wantErr:    true,
			errContain: "remove_context_document is only supported for datasets, glossaryTerms, glossaryNodes, and containers",
		},
		{
			name:       "remove_context_document on tag (unsupported)",
			urn:        tagURN,
			change:     ApplyChange{ChangeType: "remove_context_document", Target: "doc-123"},
			wantErr:    true,
			errContain: "remove_context_document is only supported for datasets, glossaryTerms, glossaryNodes, and containers",
		},
		{
			name:       "add_context_document on dataProduct (unsupported)",
			urn:        dataProductURN,
			change:     ApplyChange{ChangeType: "add_context_document", Target: "Title", Detail: "content"},
			wantErr:    true,
			errContain: "add_context_document is only supported for datasets, glossaryTerms, glossaryNodes, and containers",
		},

		// Invalid URN
		{
			name:       "invalid URN",
			urn:        "not-a-urn",
			change:     ApplyChange{ChangeType: "add_tag", Detail: "tag1"},
			wantErr:    true,
			errContain: "invalid URN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEntityTypeForChange(tc.urn, tc.change)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContain)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSupportedOpsForType(t *testing.T) {
	tests := []struct {
		entityType   string
		wantContains []string
		wantMissing  []string
	}{
		{
			entityType:   "dataset",
			wantContains: []string{"update_description", "add_tag", "remove_tag", "add_glossary_term", "add_documentation", "flag_quality_issue", "add_curated_query", "add_context_document", "update_context_document", "remove_context_document"},
		},
		{
			entityType:   "domain",
			wantContains: []string{"update_description", "add_tag", "remove_tag", "add_glossary_term", "add_documentation", "flag_quality_issue"},
			wantMissing:  []string{"add_curated_query", "add_context_document", "update_context_document", "remove_context_document"},
		},
		{
			entityType:   "tag",
			wantContains: []string{"add_tag", "remove_tag", "add_glossary_term"},
			wantMissing:  []string{"update_description", "add_curated_query", "add_context_document", "update_context_document", "remove_context_document"},
		},
		{
			entityType:   "glossaryTerm",
			wantContains: []string{"update_description", "add_tag", "add_context_document", "update_context_document", "remove_context_document"},
			wantMissing:  []string{"add_curated_query"},
		},
		{
			entityType:   "container",
			wantContains: []string{"add_tag", "add_context_document", "update_context_document", "remove_context_document"},
			wantMissing:  []string{"add_curated_query"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.entityType, func(t *testing.T) {
			ops := supportedOpsForType(tc.entityType)
			for _, want := range tc.wantContains {
				assert.Contains(t, ops, want)
			}
			for _, notWant := range tc.wantMissing {
				assert.NotContains(t, ops, notWant)
			}
		})
	}
}

func TestWrapDescriptionError(t *testing.T) {
	t.Run("wraps unsupported entity type", func(t *testing.T) {
		err := dhclient.ErrUnsupportedEntityType
		got := wrapDescriptionError(err, "urn:li:tag:pii")
		require.Error(t, got)
		assert.Contains(t, got.Error(), "update_description is not supported for tag entities")
	})

	t.Run("wraps generic error", func(t *testing.T) {
		err := errors.New("network timeout")
		got := wrapDescriptionError(err, "urn:li:dataset:(urn:li:dataPlatform:trino,x,PROD)")
		require.Error(t, got)
		assert.Contains(t, got.Error(), "description update:")
		assert.Contains(t, got.Error(), "network timeout")
	})
}

func TestWrapUnsupportedEntityTypeError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.Nil(t, wrapUnsupportedEntityTypeError(nil, "urn:li:tag:pii"))
	})

	t.Run("non-sentinel error passes through", func(t *testing.T) {
		orig := errors.New("network timeout")
		got := wrapUnsupportedEntityTypeError(orig, "urn:li:dataset:(urn:li:dataPlatform:trino,x,PROD)")
		assert.Equal(t, orig, got)
	})

	t.Run("wraps ErrUnsupportedEntityType with entity info", func(t *testing.T) {
		orig := dhclient.ErrUnsupportedEntityType
		got := wrapUnsupportedEntityTypeError(orig, "urn:li:tag:pii")
		require.Error(t, got)
		assert.Contains(t, got.Error(), "update_description is not supported for tag entities")
		assert.Contains(t, got.Error(), "Supported operations for tag")
		// Should NOT be the same error object but must preserve the error chain.
		assert.NotEqual(t, orig, got)
		assert.True(t, errors.Is(got, dhclient.ErrUnsupportedEntityType), "wrapped error should preserve error chain")
	})

	t.Run("wrapped ErrUnsupportedEntityType", func(t *testing.T) {
		wrapped := errors.Join(dhclient.ErrUnsupportedEntityType, errors.New("extra context"))
		got := wrapUnsupportedEntityTypeError(wrapped, "urn:li:tag:pii")
		assert.Contains(t, got.Error(), "update_description is not supported for tag entities")
	})

	t.Run("invalid URN falls back to original error", func(t *testing.T) {
		orig := dhclient.ErrUnsupportedEntityType
		got := wrapUnsupportedEntityTypeError(orig, "not-a-urn")
		assert.Equal(t, orig, got)
	})
}
