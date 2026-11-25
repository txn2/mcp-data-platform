//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Issue #2813: list_connections summarizes every connection by default, the
// tools of each kind named once under kind_tools, and gives one connection in
// full when named.
//
// Wire forms: list_connections takes an empty object, and connection as a
// string, each sent in that one form. No parameter touched admits a second
// form.

// TestIssue2813_TheSummaryNamesEveryConnectionAndTheToolsOfEachKind: every
// connection is named in a summary well under 16 KB, each kind has its tools.
func TestIssue2813_TheSummaryNamesEveryConnectionAndTheToolsOfEachKind(t *testing.T) {
	c := connect(t)
	_, text, err := c.callRaw("list_connections", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(text) >= 16*1024 {
		t.Errorf("the summary is %d bytes", len(text))
	}
	var out struct {
		Form        string              `json:"form"`
		Count       int                 `json:"count"`
		Connections []map[string]any    `json:"connections"`
		KindTools   map[string][]string `json:"kind_tools"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if out.Form != "summary" || out.Count != len(out.Connections) || out.Count < 5 {
		t.Fatalf("form %q count %d connections %d", out.Form, out.Count, len(out.Connections))
	}
	for _, conn := range out.Connections {
		if conn["knowledge_pages"] != nil {
			t.Errorf("%v carries its page sample in the summary", conn["name"])
		}
		if desc, _ := conn["description"].(string); strings.Count(desc, ". ") > 0 {
			t.Errorf("%v's description is more than its first sentence: %q", conn["name"], desc)
		}
	}
	if !strings.Contains(strings.Join(out.KindTools["trino"], ","), "trino_query") {
		t.Errorf("kind_tools does not name trino_query for trino: %v", out.KindTools)
	}
}

// TestIssue2813_ANamedConnectionInFull: list_connections with a connection's
// name returns its full description; an unknown name is refused naming the
// connections that exist.
func TestIssue2813_ANamedConnectionInFull(t *testing.T) {
	c := connect(t)
	out := c.call("list_connections", map[string]any{"connection": "acme"})
	conns, _ := out["connections"].([]any)
	if out["form"] != "detail" || len(conns) != 1 {
		t.Fatalf("detail = %v", out)
	}
	res, text, err := c.callRaw("list_connections", map[string]any{"connection": "no-such-connection-2813"})
	if err != nil || !res.IsError || !strings.Contains(text, "acme") {
		t.Errorf("an unknown name: %v %s", err, text)
	}
}
