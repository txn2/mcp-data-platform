//go:build integration

package acceptance

// Issue #1692: most of the platform's own tools registered an mcp.Tool with no
// Annotations field, so tools/list carried no annotations object for them. A
// client that honors readOnlyHint is told to assume a tool is not read-only
// when the hint is absent, so it treated platform_info, search and fetch --
// the three calls the platform's own instructions require first, in that
// order -- as write actions and asked the user to approve each one.
//
// The criteria are read where the defect showed: tools/list, over a real MCP
// session. The annotation an agent's client acts on is the one on the wire, so
// no criterion reads a Go struct: a middleware that rebuilt a listed tool
// would drop the field with every unit test still green.
//
// Wire forms: tools/list takes no parameters, and this ticket changes no tool
// parameter, so there is one wire form and every criterion sends it. The
// admin tool listing #1691 also reads is a GET with no body.
//
// Gateway-proxied tools (a name carrying the "__" namespace separator) are
// outside the criteria: the gateway passes an upstream's annotations through
// verbatim, and an upstream that publishes none leaves nil here. What that
// upstream advertises is the upstream's to fix.

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/test/toolsurface"
)

// issue1692Expected classifies every tool this repository registers. The
// table lives in test/toolsurface, where the real-database gate every pull
// request runs reads it too (#2521); a tool present in tools/list and absent
// from it fails the first criterion, so a toolkit added later cannot ship
// without the decision being made.
var issue1692Expected = toolsurface.Annotations

// issue1692Native returns the tools this repository is answerable for: what
// the session lists, less the gateway's proxied upstream tools.
func issue1692Native(t *testing.T, c *client) []*mcp.Tool {
	t.Helper()
	var native []*mcp.Tool
	for _, tool := range c.tools() {
		if strings.Contains(tool.Name, "__") {
			continue
		}
		native = append(native, tool)
	}
	if len(native) == 0 {
		t.Fatal("tools/list carries no platform tools")
	}
	return native
}

// issue1692Destructive reports what a listed tool says about destructiveHint.
// The field is a pointer because the specification's default when it is absent
// is true, so "unset" and "false" are different answers and the criteria
// require the field to be present on a write tool rather than inherited.
func issue1692Destructive(tool *mcp.Tool) (value, present bool) {
	if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil {
		return false, false
	}
	return *tool.Annotations.DestructiveHint, true
}

// TestIssue1692_EveryListedToolCarriesAnnotations is the defect itself: a tool
// listed with no annotations object is the one a strict client treats as a
// write. It also holds the table above to the inventory, so a tool this
// repository adds later is classified rather than quietly unannotated.
func TestIssue1692_EveryListedToolCarriesAnnotations(t *testing.T) {
	c := connect(t)

	for _, tool := range issue1692Native(t, c) {
		if tool.Annotations == nil {
			t.Errorf("%s is listed with no annotations object", tool.Name)
			continue
		}
		if _, ok := issue1692Expected[tool.Name]; !ok {
			t.Errorf("%s is listed and this ticket's table does not say whether it is read-only", tool.Name)
		}
	}
}

// TestIssue1692_ReadToolsAdvertiseReadOnly is the user-visible half. The
// platform's own instructions require platform_info, then search, then a
// scoped tool; on a client that honors the hint, each of those was a
// confirmation prompt before any data came back.
func TestIssue1692_ReadToolsAdvertiseReadOnly(t *testing.T) {
	c := connect(t)

	// The three the opening sequence requires are present on every
	// deployment, so their absence is a failure rather than a skip.
	required := map[string]bool{"platform_info": true, "search": true, "fetch": true}

	for _, tool := range issue1692Native(t, c) {
		want, ok := issue1692Expected[tool.Name]
		if !ok || !want.ReadOnly {
			continue
		}
		delete(required, tool.Name)
		if tool.Annotations == nil {
			t.Errorf("%s is listed with no annotations object", tool.Name)
			continue
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s reads and does not advertise readOnlyHint: true", tool.Name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Errorf("%s reads and does not advertise idempotentHint: true", tool.Name)
		}
	}
	for name := range required {
		t.Errorf("%s is not listed on this deployment", name)
	}
}

// TestIssue1692_WriteToolsAdvertiseTheirReach is the other half: a tool that
// can modify state says so, and says whether it can remove or overwrite, so
// the prompt a client does raise carries information.
func TestIssue1692_WriteToolsAdvertiseTheirReach(t *testing.T) {
	c := connect(t)

	for _, tool := range issue1692Native(t, c) {
		want, ok := issue1692Expected[tool.Name]
		if !ok || want.ReadOnly {
			continue
		}
		if tool.Annotations == nil {
			t.Errorf("%s is listed with no annotations object", tool.Name)
			continue
		}
		if tool.Annotations.ReadOnlyHint {
			t.Errorf("%s can modify state and advertises readOnlyHint: true", tool.Name)
		}
		got, present := issue1692Destructive(tool)
		if !present {
			t.Errorf("%s writes and advertises no destructiveHint", tool.Name)
			continue
		}
		if got != want.Destructive {
			t.Errorf("%s advertises destructiveHint: %t, want %t", tool.Name, got, want.Destructive)
		}
	}
}
