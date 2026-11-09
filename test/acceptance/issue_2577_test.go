//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
)

// Issue #2577: a new grant answered with the zero time, because nothing read
// the row's created_at back.
//
// Wire forms: manage_site takes command, site, email and level as strings; the
// PUT route's body is a JSON object whose email and level are strings.

const zeroTime2577 = "0001-01-01T00:00:00Z"

// grantedAt reads the time manage_site get shows for email.
func grantedAt2577(t *testing.T, c *client, site, email string) string {
	t.Helper()
	got := c.call("manage_site", map[string]any{"command": "get", "site": site})
	grants, _ := got["grants"].([]any)
	for _, g := range grants {
		row, _ := g.(map[string]any)
		if row["email"] == email {
			return fmt.Sprint(row["created_at"])
		}
	}
	t.Fatalf("manage_site get does not list %s: %v", email, got)
	return ""
}

func TestIssue2577_AGrantAnswersWithTheTimeTheRowWasWritten(t *testing.T) {
	s := newSite2559(t, keyEngineer2559)
	c := connectAs(t, keyEngineer2559)

	byTool := c.call("manage_site", map[string]any{"command": "grant", "site": s.name, "email": "tool2577@example.com", "level": "view"})
	if at := fmt.Sprint(byTool["created_at"]); at == zeroTime2577 || at == "<nil>" {
		t.Errorf("manage_site grant answered created_at %q", at)
	} else if read := grantedAt2577(t, c, s.name, "tool2577@example.com"); read != at {
		t.Errorf("manage_site grant said %q and manage_site get shows %q", at, read)
	}

	status, byRoute := rest2559(t, keyEngineer2559, http.MethodPut, s.path("/access"), map[string]any{"email": "route2577@example.com", "level": "view"})
	want2559(t, "granting by the route", status, http.StatusOK, byRoute)
	if at := fmt.Sprint(byRoute["created_at"]); at == zeroTime2577 || at == "<nil>" {
		t.Errorf("the route answered created_at %q", at)
	} else if read := grantedAt2577(t, c, s.name, "route2577@example.com"); read != at {
		t.Errorf("the route said %q and manage_site get shows %q", at, read)
	}
}
