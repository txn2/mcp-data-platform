//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2271: an api connection saved with no catalog is saved, and
// the save says what that means and how to fix it; one saved with a catalog
// says nothing; and a connection with no catalog keeps working by method and
// path.
//
// The admin API takes one JSON object per route. api_invoke_endpoint's body is
// not touched here: the call is a GET.

const issue2271Purpose = "Acceptance for #2271: a connection with no catalog still answers by method and path."

// issue2271Save saves an api connection over the api-test fixture and returns
// the status and the answer. The connection is removed when the test ends.
func issue2271Save(t *testing.T, c *client, name, catalogID string) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"config":      fixtureConnectionConfig(name, catalogID),
		"description": "Acceptance 2271: an api connection over the api-test fixture.",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, out := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, bytes.NewReader(raw))
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
	return status, out
}

// TestIssue2271_SavingWithNoCatalogWarns is the first criterion: the save
// succeeds and answers with a warning naming the fix.
func TestIssue2271_SavingWithNoCatalogWarns(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2271-bare-%d", time.Now().UnixNano())
	status, out := issue2271Save(t, c, name, "")
	if status != http.StatusOK {
		t.Fatalf("saving an api connection with no catalog: HTTP %d %v, want 200", status, out)
	}
	warnings, _ := out["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("the save answered warnings %v, want one", out["warnings"])
	}
	w := fmt.Sprint(warnings[0])
	for _, want := range []string{"No catalog is linked", "api_discover", "catalog_id", "/api/v1/admin/api-catalogs"} {
		if !strings.Contains(w, want) {
			t.Errorf("the warning %q does not name %q", w, want)
		}
	}
}

// TestIssue2271_SavingWithACatalogDoesNotWarn is the second half of the first
// criterion.
func TestIssue2271_SavingWithACatalogDoesNotWarn(t *testing.T) {
	c := connect(t)
	stamp := time.Now().UnixNano()
	catalogID := fmt.Sprintf("acc-2271-%d", stamp)
	if status := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": catalogID, "name": catalogID, "display_name": "Acceptance 2271",
		"description": "A catalog for the #2271 acceptance run.",
	}); status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create catalog: HTTP %d", status)
	}
	name := fmt.Sprintf("acc-2271-cat-%d", stamp)
	status, out := issue2271Save(t, c, name, catalogID)
	// The connection is removed before the catalog it references.
	t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
	if status != http.StatusOK {
		t.Fatalf("saving an api connection with a catalog: HTTP %d %v, want 200", status, out)
	}
	if _, present := out["warnings"]; present {
		t.Fatalf("a connection with a catalog answered warnings %v", out["warnings"])
	}
}

// TestIssue2271_ANoCatalogConnectionKeepsWorking is the last criterion: a
// connection deliberately left without a catalog answers a call by method and
// path through the real client, and api_discover says it has no operations,
// which is what the warning told the operator.
func TestIssue2271_ANoCatalogConnectionKeepsWorking(t *testing.T) {
	c := connect(t)
	name := fmt.Sprintf("acc-2271-raw-%d", time.Now().UnixNano())
	if status, out := issue2271Save(t, c, name, ""); status != http.StatusOK {
		t.Fatalf("saving the connection: HTTP %d %v", status, out)
	}
	waitFor(t, "the connection answers by method and path", func() (bool, string) {
		res, text, err := c.callRaw("api_invoke_endpoint", map[string]any{
			"connection": name, "method": "GET", "path": "/v1/whoami", "purpose": issue2271Purpose,
		})
		if err != nil || res.IsError {
			return false, fmt.Sprint(err, text)
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(text), &out)
		return fmt.Sprint(out["status"]) == "200", text
	})
	disc := c.call("api_discover", map[string]any{"connection": name, "purpose": issue2271Purpose})
	if ops, _ := disc["operations"].([]any); len(ops) != 0 {
		t.Fatalf("api_discover listed %d operations for a connection with no catalog", len(ops))
	}
}
