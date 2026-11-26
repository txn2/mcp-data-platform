//go:build integration

package acceptance

// Issue #2794: under protocol revision 2026-07-28 every list result carries a
// cacheScope, and "public" lets a shared cache serve one caller's result to
// another. The lists filtered by caller read "private"; the template list,
// which is the same for everyone, reads "public".
//
// Wire forms: tools/list, prompts/list, resources/list and
// resources/templates/list sent with empty params ({}) by an administrator and
// by an analyst through the real client; none takes an argument.

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestIssue2794_ListsFilteredByCallerArePrivate: an analyst and an
// administrator each list tools, prompts and resources, and each result reads
// cacheScope "private".
func TestIssue2794_ListsFilteredByCallerArePrivate(t *testing.T) {
	for _, who := range []struct{ name, key string }{{"admin", devAPIKey()}, {"analyst", "acme-analyst-key"}} {
		c := connectAs(t, who.key)
		tools, err := c.session.ListTools(c.ctx, &mcp.ListToolsParams{})
		if err != nil {
			t.Fatalf("%s tools/list: %v", who.name, err)
		}
		prompts, err := c.session.ListPrompts(c.ctx, &mcp.ListPromptsParams{})
		if err != nil {
			t.Fatalf("%s prompts/list: %v", who.name, err)
		}
		resources, err := c.session.ListResources(c.ctx, &mcp.ListResourcesParams{})
		if err != nil {
			t.Fatalf("%s resources/list: %v", who.name, err)
		}
		for method, scope := range map[string]string{
			"tools/list":     tools.CacheScope,
			"prompts/list":   prompts.CacheScope,
			"resources/list": resources.CacheScope,
		} {
			if scope != "private" {
				t.Errorf("%s %s: cacheScope %q, want private", who.name, method, scope)
			}
		}
	}
}

// TestIssue2794_TheTemplateListIsPublic: the resource template list is the
// same for every caller and reads cacheScope "public".
func TestIssue2794_TheTemplateListIsPublic(t *testing.T) {
	for _, key := range []string{devAPIKey(), "acme-analyst-key"} {
		c := connectAs(t, key)
		res, err := c.session.ListResourceTemplates(c.ctx, &mcp.ListResourceTemplatesParams{})
		if err != nil {
			t.Fatalf("resources/templates/list: %v", err)
		}
		if res.CacheScope != "public" {
			t.Errorf("resources/templates/list: cacheScope %q, want public", res.CacheScope)
		}
	}
}
