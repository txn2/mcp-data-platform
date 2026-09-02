//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1605: fetch answered found:true for a catalog or governance URN that
// does not exist, returning a document synthesized from the URN it was handed.
// The tool's own description promises the opposite ("A reference that is stale,
// unknown, or outside what you can access returns found=false rather than an
// error, so a dangling citation is a clean answer"), so an agent following a
// stale citation was told it resolved and handed a record whose every field it
// had supplied itself.
//
// The cause is upstream and is filed as txn2/mcp-datahub#204: DataHub hydrates
// a stub from the key aspect for a URN that was never ingested, echoing the URN
// and deriving a name from it with no GraphQL error, so the three by-URN reads'
// not-found test (an empty URN in the response) can never fire. This suite is
// the platform's own guard: a record carrying nothing beyond what its URN
// supplies is not a hit, whatever the catalog reports.
//
// Every criterion runs through the real surface against the dev stack attached
// to a local DataHub (DATAHUB_ENABLED=true DATAHUB_ENDPOINT=... make dev). The
// dangling references are built with a per-run stamp so they name entities that
// cannot exist rather than entities that merely happen to be absent today.
//
// Wire forms: fetch declares one parameter, "reference", as {"type":"string"}
// under additionalProperties:false, so a string is the only JSON form the
// schema admits and every call below sends it as a literal string.

const issue1605Purpose = "Acceptance for #1605: a dangling catalog or governance citation answers found:false."

// issue1605DatasetURN is a dataset dev/seed-datahub.sh ingests. It is the
// dataset control: the rule must not turn a real record into a miss.
const issue1605DatasetURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"

// issue1605Dangling builds the four reference forms that name an entity which
// was never ingested, one per by-URN arm plus the two enumeration-resolved
// governance kinds, which are here as a regression guard rather than a fix.
func issue1605Dangling(stamp string) []struct{ kind, ref string } {
	return []struct{ kind, ref string }{
		{"data product", "urn:li:dataProduct:qa-1605-nonexistent-" + stamp},
		{"glossary term", "urn:li:glossaryTerm:Qa1605Nonexistent" + stamp},
		{"dataset", fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.qa_1605_nonexistent_%s,PROD)", stamp)},
		{"tag", "urn:li:tag:qa-1605-nonexistent-" + stamp},
		{"domain", "urn:li:domain:qa-1605-nonexistent-" + stamp},
	}
}

// issue1605Stamp is a per-run suffix, so a dangling reference names an entity
// that has never been ingested rather than one that is merely absent today.
func issue1605Stamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// TestIssue1605_DanglingCitationIsNotFound is acceptance 1, 2 and 3: a data
// product, a glossary term and a dataset URN that names nothing each answer
// found:false, and carry no document to mistake for a record.
func TestIssue1605_DanglingCitationIsNotFound(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
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
// must not turn a real record into a miss. Each arm is exercised on an entity
// that does exist, and the fetched record has to carry the field the rule keys
// on so the check would fail if the arm had started refusing real entities.
func TestIssue1605_RealEntitiesStillResolve(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	stamp := issue1605Stamp()

	termURN := created(t, c, map[string]any{
		"what": "glossary_term", "name": "Reorder Point " + stamp,
		"description": "The inventory level at which a store reorders a product.",
	})
	t.Cleanup(func() { deleteEntity(c, "glossary_entity", termURN) })

	// A data product is created into a domain, so the domain comes first.
	domainURN := created(t, c, map[string]any{
		"what": "domain", "name": "Acceptance Domain " + stamp,
		"description": "Domain for the #1605 acceptance run.",
	})
	t.Cleanup(func() { deleteEntity(c, "domain", domainURN) })

	productURN := created(t, c, map[string]any{
		"what": "data_product", "name": "Retail 360 " + stamp,
		"description": "Everything about a retail day.", "domain_urn": domainURN,
	})
	t.Cleanup(func() { deleteEntity(c, "data_product", productURN) })

	for _, tc := range []struct{ kind, ref, nameWant string }{
		{"dataset", issue1605DatasetURN, "daily_sales"},
		{"glossary term", termURN, "Reorder Point " + stamp},
		{"data product", productURN, "Retail 360 " + stamp},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			content := fetchContent(t, c, tc.ref)
			name, _ := content["name"].(string)
			if !strings.Contains(name, tc.nameWant) {
				t.Errorf("fetched %s carries name %q, want it to contain %q: %v", tc.kind, name, tc.nameWant, content)
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
	requireDataHub(t, c)
	ref := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.qa_1605_shell_%s,PROD)", issue1605Stamp())

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
// the other door. The catalog's entity arm resolved a URN the same way fetch
// did, so a search on a reference that names nothing reported one match and
// handed back a citation. With fetch fixed and search not, the two contradict
// each other: search finds it, fetch says it does not exist.
func TestIssue1605_SearchByEntityURNDoesNotInventAMatch(t *testing.T) {
	c := connect(t)
	requireDataHub(t, c)
	ref := fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,warehouse.public.qa_1605_search_%s,PROD)", issue1605Stamp())

	out := c.call("search", map[string]any{
		"entity_urns": []string{ref},
		"purpose":     issue1605Purpose,
	})
	if count, ok := out["count"].(float64); ok && count != 0 {
		t.Errorf("search by a URN that names nothing reported %v match(es): %v", count, out)
	}
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		group, _ := g.(map[string]any)
		hits, _ := group["hits"].([]any)
		if len(hits) > 0 {
			t.Errorf("search invented %d hit(s) for a URN that names nothing: %v", len(hits), group)
		}
	}

	// The control: a dataset that does exist is still found by its URN, so the
	// rule has not simply closed the entity arm.
	real := c.call("search", map[string]any{
		"entity_urns": []string{issue1605DatasetURN},
		"purpose":     issue1605Purpose,
	})
	if count, _ := real["count"].(float64); count < 1 {
		t.Errorf("a dataset that exists was not found by its own URN: %v", real)
	}
}
