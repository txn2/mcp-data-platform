//go:build integration

package helpers

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EnrichmentResult represents parsed enrichment data from tool results.
type EnrichmentResult struct {
	SemanticContext *SemanticContextEnrichment `json:"semantic_context,omitempty"`
	QueryContext    map[string]*QueryContext   `json:"query_context,omitempty"`
	StorageContext  map[string]*StorageContext `json:"storage_context,omitempty"`
}

// SemanticContextEnrichment represents semantic enrichment data.
type SemanticContextEnrichment struct {
	Description      string                  `json:"description,omitempty"`
	Owners           []OwnerInfo             `json:"owners,omitempty"`
	Tags             []string                `json:"tags,omitempty"`
	Domain           *DomainInfo             `json:"domain,omitempty"`
	QualityScore     *float64                `json:"quality_score,omitempty"`
	Deprecation      *DeprecationInfo        `json:"deprecation,omitempty"`
	MatchingDatasets []MatchingDatasetResult `json:"matching_datasets,omitempty"`
}

// OwnerInfo represents an owner in the semantic context.
type OwnerInfo struct {
	URN   string `json:"urn"`
	Type  string `json:"type"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// DomainInfo represents domain information.
type DomainInfo struct {
	Name        string `json:"name,omitempty"`
	URN         string `json:"urn,omitempty"`
	Description string `json:"description,omitempty"`
}

// DeprecationInfo represents deprecation information.
type DeprecationInfo struct {
	Deprecated       bool   `json:"deprecated"`
	DecommissionTime int64  `json:"decommission_time,omitempty"`
	Note             string `json:"note,omitempty"`
}

// QueryContext represents query enrichment data for a URN.
type QueryContext struct {
	Available     bool   `json:"available"`
	EstimatedRows int64  `json:"estimated_rows,omitempty"`
	Catalog       string `json:"catalog,omitempty"`
	Schema        string `json:"schema,omitempty"`
	Table         string `json:"table,omitempty"`
}

// StorageContext represents storage enrichment data for a URN.
type StorageContext struct {
	Available   bool   `json:"available"`
	ObjectCount int64  `json:"object_count,omitempty"`
	TotalSize   int64  `json:"total_size,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
}

// MatchingDatasetResult represents a matching dataset in S3 enrichment.
type MatchingDatasetResult struct {
	URN         string           `json:"urn"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Owners      []string         `json:"owners,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	Domain      string           `json:"domain,omitempty"`
	Deprecation *DeprecationInfo `json:"deprecation,omitempty"`
}

// ExtractEnrichment extracts enrichment data from a tool result.
func ExtractEnrichment(result *mcp.CallToolResult) (*EnrichmentResult, error) {
	if result == nil {
		return nil, fmt.Errorf("result is nil")
	}

	enrichment := &EnrichmentResult{}

	for _, content := range result.Content {
		textContent, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		parseEnrichmentContent(textContent.Text, enrichment)
	}

	return enrichment, nil
}

// parseEnrichmentContent parses a text content into enrichment result.
func parseEnrichmentContent(text string, enrichment *EnrichmentResult) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return
	}

	parseSemanticContext(parsed, enrichment)
	parseQueryContext(parsed, enrichment)
	parseStorageContext(parsed, enrichment)
}

// parseSemanticContext extracts semantic context from parsed JSON.
func parseSemanticContext(parsed map[string]json.RawMessage, enrichment *EnrichmentResult) {
	raw, ok := parsed["semantic_context"]
	if !ok {
		return
	}
	var sc SemanticContextEnrichment
	if err := json.Unmarshal(raw, &sc); err == nil {
		enrichment.SemanticContext = &sc
	}
}

// parseQueryContext extracts query context from parsed JSON.
func parseQueryContext(parsed map[string]json.RawMessage, enrichment *EnrichmentResult) {
	raw, ok := parsed["query_context"]
	if !ok {
		return
	}
	var qc map[string]*QueryContext
	if err := json.Unmarshal(raw, &qc); err == nil {
		enrichment.QueryContext = qc
	}
}

// parseStorageContext extracts storage context from parsed JSON.
func parseStorageContext(parsed map[string]json.RawMessage, enrichment *EnrichmentResult) {
	raw, ok := parsed["storage_context"]
	if !ok {
		return
	}
	var stc map[string]*StorageContext
	if err := json.Unmarshal(raw, &stc); err == nil {
		enrichment.StorageContext = stc
	}
}

// AssertHasSemanticContext asserts that the result has semantic context enrichment.
func AssertHasSemanticContext(t *testing.T, result *mcp.CallToolResult) *SemanticContextEnrichment {
	t.Helper()

	enrichment, err := ExtractEnrichment(result)
	if err != nil {
		t.Fatalf("failed to extract enrichment: %v", err)
	}

	if enrichment.SemanticContext == nil {
		t.Fatal("expected semantic_context in result, but not found")
	}

	return enrichment.SemanticContext
}

// AssertHasQueryContext asserts that the result has query context enrichment.
func AssertHasQueryContext(t *testing.T, result *mcp.CallToolResult) map[string]*QueryContext {
	t.Helper()

	enrichment, err := ExtractEnrichment(result)
	if err != nil {
		t.Fatalf("failed to extract enrichment: %v", err)
	}

	if len(enrichment.QueryContext) == 0 {
		t.Fatal("expected query_context in result, but not found")
	}

	return enrichment.QueryContext
}

// AssertHasStorageContext asserts that the result has storage context enrichment.
func AssertHasStorageContext(t *testing.T, result *mcp.CallToolResult) map[string]*StorageContext {
	t.Helper()

	enrichment, err := ExtractEnrichment(result)
	if err != nil {
		t.Fatalf("failed to extract enrichment: %v", err)
	}

	if len(enrichment.StorageContext) == 0 {
		t.Fatal("expected storage_context in result, but not found")
	}

	return enrichment.StorageContext
}

// AssertNoEnrichment asserts that the result has no enrichment.
func AssertNoEnrichment(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()

	enrichment, err := ExtractEnrichment(result)
	if err != nil {
		t.Fatalf("failed to extract enrichment: %v", err)
	}

	if enrichment.SemanticContext != nil || len(enrichment.QueryContext) > 0 || len(enrichment.StorageContext) > 0 {
		t.Fatal("expected no enrichment in result, but found some")
	}
}

// AssertOwnerPresent asserts that an owner URN is present in the semantic context.
func AssertOwnerPresent(t *testing.T, sc *SemanticContextEnrichment, ownerURN string) {
	t.Helper()

	for _, owner := range sc.Owners {
		if owner.URN == ownerURN {
			return
		}
	}
	// Build a list of owner URNs for the error message
	ownerURNs := make([]string, len(sc.Owners))
	for i, o := range sc.Owners {
		ownerURNs[i] = o.URN
	}
	t.Errorf("expected owner %q in semantic context, but not found. owners: %v", ownerURN, ownerURNs)
}

// AssertTagPresent asserts that a tag is present in the semantic context.
func AssertTagPresent(t *testing.T, sc *SemanticContextEnrichment, tagName string) {
	t.Helper()

	for _, tag := range sc.Tags {
		if tag == tagName {
			return
		}
	}
	t.Errorf("expected tag %q in semantic context, but not found. tags: %v", tagName, sc.Tags)
}

// AssertIsDeprecated asserts that the semantic context indicates deprecation.
func AssertIsDeprecated(t *testing.T, sc *SemanticContextEnrichment) {
	t.Helper()

	if sc.Deprecation == nil {
		t.Fatal("expected deprecation info in semantic context, but not found")
	}

	if !sc.Deprecation.Deprecated {
		t.Error("expected deprecated=true, but got false")
	}
}

// AssertQueryAvailable asserts that a URN is available for querying.
func AssertQueryAvailable(t *testing.T, qc map[string]*QueryContext, urn string) {
	t.Helper()

	ctx, ok := qc[urn]
	if !ok {
		t.Fatalf("URN %q not found in query context", urn)
	}

	if !ctx.Available {
		t.Errorf("expected URN %q to be available for querying", urn)
	}
}

// AssertStorageAvailable asserts that a URN has storage available.
func AssertStorageAvailable(t *testing.T, sc map[string]*StorageContext, urn string) {
	t.Helper()

	ctx, ok := sc[urn]
	if !ok {
		t.Fatalf("URN %q not found in storage context", urn)
	}

	if !ctx.Available {
		t.Errorf("expected URN %q to have storage available", urn)
	}
}

// AssertMatchingDatasetCount asserts the number of matching datasets.
func AssertMatchingDatasetCount(t *testing.T, sc *SemanticContextEnrichment, expected int) {
	t.Helper()

	if len(sc.MatchingDatasets) != expected {
		t.Errorf("expected %d matching datasets, got %d", expected, len(sc.MatchingDatasets))
	}
}

// DiffToolNames compares a listing of tool names against the exact set expected,
// returning what the listing lacks and what it carries beyond the set. Both are
// sorted, so a caller reports the same message on every run.
//
// The set is what a tool listing is asserted against rather than a count: a
// count goes stale the moment the platform starts reporting a tool it always
// registered, and it names neither the tool that went missing nor the one that
// leaked (#1644).
func DiffToolNames(got, want []string) (missing, unexpected []string) {
	wanted := make(map[string]bool, len(want))
	for _, name := range want {
		wanted[name] = true
	}
	seen := make(map[string]bool, len(got))
	for _, name := range got {
		seen[name] = true
		if !wanted[name] {
			unexpected = append(unexpected, name)
		}
	}
	for _, name := range want {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	return missing, unexpected
}

// AssertToolSet asserts that a tool listing is exactly the named set, reporting
// any tool the listing lacks and any it carries beyond the set by name.
func AssertToolSet(t *testing.T, got []string, want ...string) {
	t.Helper()

	missing, unexpected := DiffToolNames(got, want)
	if len(missing) > 0 {
		t.Errorf("tool listing is missing %v (listed: %v)", missing, got)
	}
	if len(unexpected) > 0 {
		t.Errorf("tool listing carries %v beyond the expected set %v", unexpected, want)
	}
}
