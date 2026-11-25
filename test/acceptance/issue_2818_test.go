//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #2818: with server.reference_scheme set, the platform's own wording
// about the reference form is written under the deployment's scheme: no tool
// description or schema in tools/list says mcp:, generic forms included, and
// fetch's description says acme:<type>:<key>. With no scheme set, nothing
// changes.
//
// Wire forms: tools/list takes no arguments; the started process reads a YAML
// configuration whose reference_scheme is a string. No parameter touched
// admits a second form.

// issue2818Config is a deployment over the dev database with reference_scheme
// acme; the first %s is the address, the second the database.
const issue2818Config = `
server:
  name: acceptance-2818
  transport: http
  address: "%%s"
  reference_scheme: acme
auth:
  api_keys:
    enabled: true
    keys:
      - key: "acceptance-2818-key"
        name: "acceptance"
        roles: ["admin"]
personas:
  admin:
    display_name: "Administrator"
    roles: ["admin"]
    tools:
      allow: ["*"]
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

// TestIssue2818_NoDescriptionSaysMcpUnderAScheme: tools/list holds no mcp:
// anywhere in a description or schema, and fetch's description names
// acme:<type>:<key>.
func TestIssue2818_NoDescriptionSaysMcpUnderAScheme(t *testing.T) {
	p := startPlatform(t, fmt.Sprintf(issue2818Config, issue1694DevDSN()))
	p.serving(t, 90*time.Second)
	c := connectAt(t, p.base, "acceptance-2818-key")
	fetchSays := false
	for _, tool := range c.tools() {
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		if i := strings.Index(string(raw), "mcp:"); i >= 0 {
			t.Errorf("%s still says mcp: ...%s...", tool.Name, string(raw)[max(0, i-60):min(len(raw), i+40)])
		}
		if tool.Name == "fetch" && strings.Contains(tool.Description, "acme:<type>:<key>") {
			fetchSays = true
		}
	}
	if !fetchSays {
		t.Error("fetch's description does not say acme:<type>:<key>")
	}
}

// TestIssue2818_WithNoSchemeNothingChanges: the dev stack, which sets no
// scheme, still describes the mcp: form.
func TestIssue2818_WithNoSchemeNothingChanges(t *testing.T) {
	for _, tool := range connect(t).tools() {
		if tool.Name == "fetch" && !strings.Contains(tool.Description, "mcp:<type>:<key>") {
			t.Errorf("fetch's description changed with no scheme set:\n%s", tool.Description)
		}
	}
}
