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
}

// SemanticContextEnrichment represents semantic enrichment data.
type SemanticContextEnrichment struct {
	Description  string           `json:"description,omitempty"`
	Owners       []OwnerInfo      `json:"owners,omitempty"`
	Tags         []string         `json:"tags,omitempty"`
	Domain       *DomainInfo      `json:"domain,omitempty"`
	QualityScore *float64         `json:"quality_score,omitempty"`
	Deprecation  *DeprecationInfo `json:"deprecation,omitempty"`
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

// AssertNoEnrichment asserts that the result has no enrichment.
func AssertNoEnrichment(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()

	enrichment, err := ExtractEnrichment(result)
	if err != nil {
		t.Fatalf("failed to extract enrichment: %v", err)
	}

	if enrichment.SemanticContext != nil {
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
