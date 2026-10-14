//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Issue #1605: fetch answered found:true for a catalog or governance
// reference that does not exist, returning a document synthesized from the
// reference it was handed. The tool's own description promises the opposite
// ("A reference that is stale, unknown, or outside what you can access returns
// found=false rather than an error, so a dangling citation is a clean
// answer"), so an agent following a stale citation was told it resolved and
// handed a record whose every field it had supplied itself.
//
// The catalog these criteria run against is the platform's own (#1916): a
// reference names an entity the catalog holds or it names nothing. Every
// criterion runs through the real surface against the dev stack, whose catalog
// dev/seed-catalog.sql seeds. The dangling references are built with a per-run
// stamp so they name entities that cannot exist rather than entities that
// merely happen to be absent today.
//
// Wire forms: fetch declares one parameter, "reference", as {"type":"string"}
// under additionalProperties:false, so a string is the only JSON form the
// schema admits and every call below sends it as a literal string. search's
// entity_urns is an array of strings and is sent as one.

const issue1605Purpose = "Acceptance for #1605: a dangling catalog or governance citation answers found:false."

// issue1605DatasetRef is a dataset dev/seed-catalog.sql writes, with a
// description. It is the dataset control: the rule must not turn a real record
// into a miss.
const issue1605DatasetRef = "mcp:dataset:(trino,acme,iceberg.retail.daily_sales)"

// issue1605Dangling builds a reference of every definition kind and a dataset
// that names nothing.
func issue1605Dangling(stamp string) []struct{ kind, ref string } {
	return []struct{ kind, ref string }{
		{"data product", "mcp:data_product:qa-1605-nonexistent-" + stamp},
		{"glossary term", "mcp:glossary_term:qa-1605-nonexistent-" + stamp},
		{"dataset", fmt.Sprintf("mcp:dataset:(trino,acme,warehouse.public.qa_1605_nonexistent_%s)", stamp)},
		{"tag", "mcp:tag:qa-1605-nonexistent-" + stamp},
		{"domain", "mcp:domain:qa-1605-nonexistent-" + stamp},
	}
}

// issue1605Stamp is a per-run suffix, so a dangling reference names an entity
// that has never been written rather than one that is merely absent today.
func issue1605Stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// issue1605Content reads a reference through fetch and returns the document's
// content, failing the test when the reference does not resolve.
func issue1605Content(t *testing.T, c *client, ref string) map[string]any {
	t.Helper()
	out := c.call("fetch", map[string]any{"reference": ref, "purpose": issue1605Purpose})
	if found, _ := out["found"].(bool); !found {
		t.Fatalf("fetch %s: not found: %v", ref, out)
	}
	doc, _ := out["document"].(map[string]any)
	content, _ := doc["content"].(map[string]any)
	if content == nil {
		t.Fatalf("fetch %s: document carries no content object: %v", ref, out)
	}
	return content
}

// issue1605Define creates a definition through the portal catalog route and
// removes it when the test ends, returning its reference.
func issue1605Define(t *testing.T, c *client, kind, name, description string) string {
	t.Helper()
	status, def := c.rest(http.MethodPost, "/api/v1/portal/catalog/definitions", jsonBody(t, map[string]any{
		"kind": kind, "name": name, "description": description,
	}))
	ref, _ := def["reference"].(string)
	if status != http.StatusCreated || ref == "" {
		t.Fatalf("creating a %s: %d %v", kind, status, def)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/portal/catalog/definition?ref="+url.QueryEscape(ref), nil)
	})
	return ref
}

// TestIssue1605_DanglingCitationIsNotFound is acceptance 1, 2 and 3: a
// reference of every kind that names nothing answers found:false, and carries
// no document to mistake for a record.
func TestIssue1605_DanglingCitationIsNotFound(t *testing.T) {
	c := connect(t)
	stamp := issue1605Stamp()

	for _, tc := range issue1605Dangling(stamp) {
		t.Run(tc.kind, func(t *testing.T) {
			out := c.call("fetch", map[string]any{"reference": tc.ref, "purpose": issue1605Purpose})
			found, _ := out["found"].(bool)
			if found {
				t.Fatalf("fetch of a %s that does not exist answered found:true: %v", tc.kind, out)
			}
			if doc, ok := out["document"]; ok && doc != nil {
				t.Errorf("a not-found answer still carries a document: %v", doc)
			}
			if msg, _ := out["message"].(string); strings.TrimSpace(msg) == "" {
				t.Errorf("a not-found answer carries no message explaining why: %v", out)
			}
			if ref, _ := out["reference"].(string); ref != tc.ref {
				t.Errorf("answer echoes reference %q, want the one asked for %q", ref, tc.ref)
			}
		})
	}
}

// TestIssue1605_RealEntitiesStillResolve is acceptance 4: the existence rule
// must not turn a real record into a miss. Each kind is exercised on an entity
// that does exist, and the fetched record has to carry its description so the
// check would fail if the arm had started refusing real entities.
func TestIssue1605_RealEntitiesStillResolve(t *testing.T) {
	c := connect(t)
	stamp := issue1605Stamp()

	termRef := issue1605Define(t, c, "glossary_term", "Reorder Point "+stamp,
		"The inventory level at which a store reorders a product.")
	productRef := issue1605Define(t, c, "data_product", "Retail 360 "+stamp, "Everything about a retail day.")

	t.Run("dataset", func(t *testing.T) {
		content := issue1605Content(t, c, issue1605DatasetRef)
		if name, _ := content["name"].(string); name != "daily_sales" {
			t.Errorf("fetched dataset carries name %q, want daily_sales: %v", name, content)
		}
		aspects, _ := content["aspects"].(map[string]any)
		if desc, _ := aspects["description"].(string); strings.TrimSpace(desc) == "" {
			t.Errorf("fetched dataset carries no description, so this control proves nothing about the rule: %v", content)
		}
	})
	for _, tc := range []struct{ kind, ref, nameWant string }{
		{"glossary term", termRef, "Reorder Point " + stamp},
		{"data product", productRef, "Retail 360 " + stamp},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			content := issue1605Content(t, c, tc.ref)
			if name, _ := content["name"].(string); name != tc.nameWant {
				t.Errorf("fetched %s carries name %q, want %q: %v", tc.kind, name, tc.nameWant, content)
			}
			if desc, _ := content["description"].(string); strings.TrimSpace(desc) == "" {
				t.Errorf("fetched %s carries no description, so this control proves nothing about the rule: %v", tc.kind, content)
			}
		})
	}
}

// TestIssue1605_DanglingDatasetDoesNotReportQueryAvailability is the sharpest
// shape of the defect: the dataset arm answered with a full record shell whose
// miss was visible only inside query_availability, so a reader who trusted
// found:true and the record's name never reached it. A dangling dataset
// reference must not produce a record at all.
func TestIssue1605_DanglingDatasetDoesNotReportQueryAvailability(t *testing.T) {
	c := connect(t)
	ref := fmt.Sprintf("mcp:dataset:(trino,acme,warehouse.public.qa_1605_shell_%s)", issue1605Stamp())

	out := c.call("fetch", map[string]any{"reference": ref, "purpose": issue1605Purpose})
	if found, _ := out["found"].(bool); found {
		t.Fatalf("a dangling dataset reference answered found:true: %v", out)
	}
	doc, _ := out["document"].(map[string]any)
	if doc == nil {
		return
	}
	content, _ := doc["content"].(map[string]any)
	if _, ok := content["query_availability"]; ok {
		t.Errorf("a not-found dataset still reports query_availability, which is the shell this issue is about: %v", content)
	}
}

// TestIssue1605_SearchByEntityURNDoesNotInventAMatch is the same defect through
// the other door: a search on a reference that names nothing must report no
// match, or search and fetch contradict each other.
func TestIssue1605_SearchByEntityURNDoesNotInventAMatch(t *testing.T) {
	c := connect(t)
	ref := fmt.Sprintf("mcp:dataset:(trino,acme,warehouse.public.qa_1605_search_%s)", issue1605Stamp())

	out := c.call("search", map[string]any{
		"entity_urns": []string{ref},
		"purpose":     issue1605Purpose,
	})
	if count, ok := out["count"].(float64); ok && count != 0 {
		t.Errorf("search by a reference that names nothing reported %v match(es): %v", count, out)
	}
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		hits, _ := group["hits"].([]any)
		if len(hits) > 0 {
			t.Errorf("search invented %d hit(s) for a reference that names nothing: %v", len(hits), group)
		}
	}

	// The control: a dataset that does exist is still found by its reference,
	// so the rule has not simply closed the entity arm.
	real := c.call("search", map[string]any{
		"entity_urns": []string{issue1605DatasetRef},
		"purpose":     issue1605Purpose,
	})
	if count, _ := real["count"].(float64); count < 1 {
		t.Errorf("a dataset that exists was not found by its own reference: %v", real)
	}
}
