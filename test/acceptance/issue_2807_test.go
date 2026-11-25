//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2807: the two tools that only opened a UI an agent never asked for
// are gone from every persona's tool list. A deployment whose persona still
// names one starts, logs one warning naming it and what replaced it, and
// serves every other tool. The portal's Prompts and Automations pages read
// the same routes as before.
//
// Wire forms: tools/list takes no arguments; the portal routes are GETs with
// no body; the started process reads a YAML configuration whose tools.allow
// is a list of strings. No parameter touched admits a second form.

// issue2807RemovedTools are the tools the ticket retires.
var issue2807RemovedTools = []string{"show_prompts", "show_scripts"}

// TestIssue2807_NoPersonaListsTheRemovedTools: tools/list holds neither tool
// for the administrator or the analyst.
func TestIssue2807_NoPersonaListsTheRemovedTools(t *testing.T) {
	for _, key := range []string{defaultDevAPIKey, "acme-analyst-key"} {
		for _, tool := range connectAs(t, key).tools() {
			for _, removed := range issue2807RemovedTools {
				if tool.Name == removed {
					t.Errorf("%s still lists %s", key, removed)
				}
			}
		}
	}
}

// TestIssue2807_ThePortalPagesStillAnswer: the Prompts and Automations pages'
// routes answer as they did.
func TestIssue2807_ThePortalPagesStillAnswer(t *testing.T) {
	c := connect(t)
	for _, path := range []string{"/api/v1/portal/prompts", "/api/v1/portal/scripts"} {
		if status, body := c.rest(http.MethodGet, path, nil); status != http.StatusOK {
			t.Errorf("GET %s = %d: %v", path, status, body)
		}
	}
}

// issue2807Config is a deployment whose persona allow list names retired
// tools, over the dev database; the first %s is the address startPlatform
// fills in, the second the database.
const issue2807Config = `
server:
  name: acceptance-2807
  transport: http
  address: "%%s"
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2807-key"
        name: "acceptance"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["platform_info", "list_connections", "search", "fetch", "show_prompts", "trino_explain", "platform_find_tools"]
    connections:
      allow: ["*"]
database:
  dsn: "%s"
scripts:
  worker:
    enabled: false
notifications:
  enabled: false
thumbnails:
  enabled: false
`

// TestIssue2807_APersonaNamingARemovedToolStillStarts: the process starts,
// logs one warning per retired tool naming what replaced it, and serves the
// tools it does have.
func TestIssue2807_APersonaNamingARemovedToolStillStarts(t *testing.T) {
	p := startPlatform(t, fmt.Sprintf(issue2807Config, issue1694DevDSN()))
	p.serving(t, 60*time.Second)
	c := connectAt(t, p.base, "acceptance-2807-key")
	names := map[string]bool{}
	for _, tool := range c.tools() {
		names[tool.Name] = true
	}
	if !names["platform_info"] || !names["list_connections"] {
		t.Errorf("the tools the persona does have were not served: %v", names)
	}
	logs := p.out.String()
	for tool, replacement := range map[string]string{
		"show_prompts": "manage_prompt", "trino_explain": "trino_query", "platform_find_tools": "search",
	} {
		lines := 0
		for line := range strings.SplitSeq(logs, "\n") {
			if strings.Contains(line, "persona_tool_unregistered") && strings.Contains(line, tool) {
				lines++
				if !strings.Contains(line, "use "+replacement) {
					t.Errorf("the warning for %s does not name %s: %s", tool, replacement, line)
				}
			}
		}
		if lines != 1 {
			t.Errorf("%d warnings name %s, want 1", lines, tool)
		}
	}
}
