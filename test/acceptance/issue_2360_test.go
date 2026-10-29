//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2360: a form request whose form_submit fails on a
// sensitive value records the failure with the value replaced, in the
// failed requests endpoint_list reports and in the request's audit row. The
// SSN is submitted as a form body and as JSON.
//
// Wire forms: manage_script create, endpoint_set and endpoint_list take
// literal tools/call params; a submission is posted as
// application/x-www-form-urlencoded and as application/json; the audit
// route is a GET with a query string.

const issue2360SSN = "987-65-4321"

// issue2360Source is a form whose form_submit reads the SSN as a number,
// which fails on a dashed one and names it in the error.
const issue2360Source = `FIELDS = [
    {"name": "name", "type": "text", "required": True},
    {"name": "ssn", "type": "text", "required": True, "sensitive": True},
]

def form_spec(req):
    """The application, and the query it was opened with as a hidden field."""
    return {"fields": FIELDS + [{"name": "t", "type": "hidden"}], "prefill": {"t": req["query"].get("t", "")}}

def form_submit(req, values):
    """Reads the SSN as a number."""
    return {"confirmation": "Thanks " + str(int(values["ssn"]))}

def main():
    """Drafts the spec."""
    platform.result({"fields": len(form_spec({"query": {}, "caller": None})["fields"])})
`

func TestIssue2360_AFailedFormRequestRecordsNoSensitiveValue(t *testing.T) {
	admin := connect(t)
	name := endpointName("acc-2360")
	admin.saveScript(map[string]any{"command": "create", "name": name, "source": issue2360Source}, nil)
	out := bindEndpoint(t, admin, name, "", map[string]any{
		"endpoint": name, "methods": []string{"GET", "POST"}, "connection": formsConn, "auth_mode": "public",
	})
	form, _ := out["form"].(map[string]any)
	t.Cleanup(func() {
		admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+fmt.Sprint(form["source"]), http.NoBody)
	})

	for _, asJSON := range []bool{false, true} {
		shown := showForm(t, admin.base, name, "", nil)
		shown.waitToFill()
		status, got := submitForm(t, admin.base, name, shown.fields(map[string]string{"name": "Dee", "ssn": issue2360SSN}), asJSON, nil)
		if status != http.StatusInternalServerError {
			t.Fatalf("a form_submit that fails answered %d %v, want 500", status, got)
		}
	}

	listed := admin.call("manage_script", map[string]any{"command": "endpoint_list", "name": name})
	failures, _ := listed["failures"].([]any)
	if len(failures) < 2 {
		t.Fatalf("endpoint_list reports %d failed requests, want both: %v", len(failures), listed)
	}
	raw, _ := json.Marshal(failures)
	if strings.Contains(string(raw), issue2360SSN) {
		t.Fatalf("a failed request holds the SSN: %s", raw)
	}
	if !strings.Contains(string(raw), "[REDACTED:ssn]") {
		t.Fatalf("the failure does not say which field it was: %s", raw)
	}

	rows := admin.list("/api/v1/admin/audit/events?event_kind=endpoint_request&per_page=100")
	audited, _ := json.Marshal(rows)
	if strings.Contains(string(audited), issue2360SSN) {
		t.Fatalf("an endpoint_request audit row holds the SSN")
	}
	if !strings.Contains(string(audited), "endpoint:"+name) {
		t.Fatalf("no endpoint_request audit row names %s", name)
	}
}
