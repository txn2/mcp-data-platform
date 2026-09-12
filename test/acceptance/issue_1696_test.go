//go:build integration

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1696: the link handed back for a knowledge page the agent just wrote
// opens the page. Before this the promotion response named the page by id and
// slug and carried no address at all, so an agent telling someone where the
// page is had to invent one, and the address it invented was not the address
// the portal opens the page at.
//
// Every criterion runs through the real MCP surface against the running stack,
// and the address the tool hands back is then read through the route the portal
// itself reads, so a link that resolves here is a link a person can open.
//
// Wire forms: `action`, `sink` and `changeset_id` are typed strings and
// `confirm` and `page.force_new` are typed booleans, each admitting exactly ONE
// JSON form; `page` and `instructions` are typed objects whose own fields are
// strings. The one form of each is what every check below sends as literal
// tools/call params.

// issue1696PagePath is the portal route a knowledge page is opened at. The
// address the tool reports must end in it, because that is what a browser
// resolves.
const issue1696PagePath = "/portal/knowledge/pages/"

// issue1696Slug is unique per run so a create is a create, not a consolidation
// onto a page an earlier run left behind.
func issue1696Slug(prefix string) string {
	return fmt.Sprintf("acceptance-1696-%s-%d", prefix, time.Now().UnixNano())
}

// rollback1696 reverts a promotion so the run leaves no page behind.
func rollback1696(t *testing.T, c *client, changesetID string) {
	t.Helper()
	t.Cleanup(func() {
		res, text, err := c.callRaw("apply_knowledge", map[string]any{
			"action": "rollback", "changeset_id": changesetID, "confirm": true,
		})
		if err != nil || res.IsError {
			t.Logf("rolling back changeset %s: %v %s", changesetID, err, text)
		}
	})
}

// TestIssue1696_APromotedPageReportsTheAddressItOpensAt is the ticket's central
// criterion: the response to a page promotion carries the address of the page,
// and reading that address returns the page that was just written.
func TestIssue1696_APromotedPageReportsTheAddressItOpensAt(t *testing.T) {
	c := connect(t)
	slug := issue1696Slug("promoted")
	title := "Acceptance 1696 promoted page"

	out := c.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug": slug, "title": title,
			"summary":   "Written by the #1696 acceptance run.",
			"body":      "The link handed back for a page must open that page.",
			"force_new": true,
		},
	})
	csID, _ := out["changeset_id"].(string)
	if csID == "" {
		t.Fatalf("the promotion recorded no changeset: %v", out)
	}
	rollback1696(t, c, csID)

	pageID, _ := out["page_id"].(string)
	if pageID == "" {
		t.Fatalf("the promotion names no page: %v", out)
	}
	portalURL, _ := out["portal_url"].(string)
	if portalURL == "" {
		t.Fatalf("the promotion hands back no address for the page it wrote: %v", out)
	}
	if want := c.base + issue1696PagePath + pageID; portalURL != want {
		t.Errorf("portal_url = %q; want the address the portal opens the page at, %q", portalURL, want)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, portalURL) {
		t.Errorf("the message does not carry the address: %q", msg)
	}

	// The address is only worth handing over if it resolves. Read the page
	// through the route the portal page itself reads.
	status, body := c.rest("GET", "/api/v1/portal/knowledge-pages/"+pageID, nil)
	if status != 200 {
		t.Fatalf("the address the tool handed back does not resolve: status %d (%v)", status, body)
	}
	if got, _ := body["title"].(string); got != title {
		t.Errorf("the address resolves to a different page: title = %q, want %q", got, title)
	}
}

// TestIssue1696_TheSlugOpensThePageToo holds the other half of the handoff: the
// slug is the only human-readable name the promotion reports, and the platform
// names pages by slug in its own shipped text and in every
// mcp:knowledge_page:<slug> reference, so the page route resolves a slug as
// well as an id. fetch already resolves either; this makes the portal read
// agree with it.
func TestIssue1696_TheSlugOpensThePageToo(t *testing.T) {
	c := connect(t)
	slug := issue1696Slug("slug-address")

	out := c.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "knowledge_page", "confirm": true,
		"page": map[string]any{
			"slug": slug, "title": "Acceptance 1696 slug address",
			"summary":   "Written by the #1696 acceptance run.",
			"body":      "A slug-shaped address opens the page it names.",
			"force_new": true,
		},
	})
	if csID, _ := out["changeset_id"].(string); csID != "" {
		rollback1696(t, c, csID)
	}
	pageID, _ := out["page_id"].(string)
	if pageID == "" {
		t.Fatalf("the promotion names no page: %v", out)
	}

	status, body := c.rest("GET", "/api/v1/portal/knowledge-pages/"+slug, nil)
	if status != 200 {
		t.Fatalf("GET the page by its slug: status %d (%v)", status, body)
	}
	if got, _ := body["id"].(string); got != pageID {
		t.Errorf("the slug resolves to page %q; want the page just written, %q", got, pageID)
	}

	// An address naming nothing is still a clean 404, so slug tolerance did not
	// turn a missing page into some other page.
	missing, _ := c.rest("GET", "/api/v1/portal/knowledge-pages/"+slug+"-not-a-page", nil)
	if missing != 404 {
		t.Errorf("an address naming nothing answered %d; want 404", missing)
	}
}

// TestIssue1696_ADivertedRuleReportsItsPageAddress covers the second way the
// platform writes a knowledge page: a rule too long for the agent-instruction
// layer is diverted onto a page, and that response must hand back the same
// address, because the person told the rule was recorded is the person who
// needs to read it.
func TestIssue1696_ADivertedRuleReportsItsPageAddress(t *testing.T) {
	c := connect(t)
	original := readInstructions(t, c)
	restoreInstructions(t, c, original)

	section := "Acceptance 1696 diverted"
	// Over the sink's inline-rule limit, so the body is diverted to a page and
	// the section holds one index entry pointing at it.
	body := strings.Repeat("This rule is longer than the inline limit for a single operating instruction. ", 32)

	out := c.call("apply_knowledge", map[string]any{
		"action": "apply", "sink": "agent_instructions", "confirm": true,
		"instructions": map[string]any{
			"section": section, "body": body,
			"slug": issue1696Slug("diverted"),
		},
	})
	pageID, _ := out["page_id"].(string)
	if pageID == "" {
		t.Fatalf("the rule was not diverted onto a page: %v", out)
	}
	portalURL, _ := out["portal_url"].(string)
	if want := c.base + issue1696PagePath + pageID; portalURL != want {
		t.Errorf("portal_url = %q; want %q", portalURL, want)
	}

	status, page := c.rest("GET", "/api/v1/portal/knowledge-pages/"+pageID, nil)
	if status != 200 {
		t.Fatalf("the diverted rule's address does not resolve: status %d (%v)", status, page)
	}
}
