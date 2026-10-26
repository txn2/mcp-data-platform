//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// Acceptance for #2180: the owner of a form reads its submissions without
// SQL, sees a new one within seconds of its acceptance, and exports them as
// CSV and as an Excel workbook; and an agent makes a contact form, binds it
// and shares an asset embedding it through MCP tool calls alone, and an
// anonymous browser submits it.
//
// Wire forms: manage_script create and endpoint_set, save_asset and
// manage_asset share take their arguments as literal tools/call params; a
// submission is a JSON object; the submissions route is a GET with limit as a
// query string and the export route a GET with format=csv and format=xlsx.

// TestIssue2180_TheOwnerSeesANewSubmissionAndExportsIt.
func TestIssue2180_TheOwnerSeesANewSubmissionAndExportsIt(t *testing.T) {
	admin := connect(t)
	form, _ := bindForm(t, admin, "acc-2180-owner", formFields, map[string]any{"auth_mode": "public"})
	scriptID := issue2179ScriptID(t, admin, form)
	shown := showForm(t, admin.base, form, "", nil)
	shown.waitToFill()
	if status, out := submitForm(t, admin.base, form, shown.fields(map[string]string{"email": "dana@example.com", "message": "=1+1"}), true, nil); status != http.StatusOK {
		t.Fatalf("the submission: %d %v", status, out)
	}
	accepted := time.Now()

	// The page's Refresh: the newest submissions, read until the new one
	// shows, which it must within seconds of being accepted.
	var page map[string]any
	for page == nil {
		got := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions?limit=20", bearer(devAPIKey()), "")
		var p map[string]any
		_ = json.Unmarshal([]byte(got.body), &p)
		if subs, _ := p["submissions"].([]any); got.status == http.StatusOK && len(subs) == 1 {
			page = p
		} else if time.Since(accepted) > 10*time.Second {
			t.Fatalf("the submission did not show within 10 s of being accepted: %d %s", got.status, got.body)
		} else {
			time.Sleep(time.Second)
		}
	}
	t.Logf("the submission showed %v after it was accepted", time.Since(accepted).Round(100*time.Millisecond))
	sub, _ := page["submissions"].([]any)[0].(map[string]any)
	values, _ := sub["values"].(map[string]any)
	if values["email"] != "dana@example.com" || page["table"] != "webhook_form_"+strings.ReplaceAll(form, "-", "_") {
		t.Fatalf("the owner's submissions: %v", page)
	}

	csvAnswer := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions/export?format=csv", bearer(devAPIKey()), "")
	if csvAnswer.status != http.StatusOK || !strings.HasPrefix(csvAnswer.body, "received_at,submitter,email,message,t") ||
		!strings.Contains(csvAnswer.body, "dana@example.com") || !strings.Contains(csvAnswer.body, "'=1+1") {
		t.Fatalf("the CSV export: %d %q", csvAnswer.status, csvAnswer.body)
	}
	xlsx := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions/export?format=xlsx", bearer(devAPIKey()), "")
	f, err := excelize.OpenReader(bytes.NewReader([]byte(xlsx.body)))
	if xlsx.status != http.StatusOK || err != nil {
		t.Fatalf("the Excel export: %d %v", xlsx.status, err)
	}
	rows, _ := f.GetRows("Submissions")
	if len(rows) != 2 || rows[1][2] != "dana@example.com" {
		t.Fatalf("the workbook holds %v", rows)
	}

	peer := endpointRequest(t, admin.base, http.MethodGet, "/api/v1/portal/scripts/"+scriptID+"/submissions", bearer(devPeerAPIKey), "")
	if peer.status != http.StatusNotFound {
		t.Fatalf("somebody who does not own the form read its submissions: %d", peer.status)
	}
}

// TestIssue2180_AnAgentMakesBindsAndSharesAFormThroughMCPAlone: every step
// is a tools/call, and an anonymous browser submits the shared form.
func TestIssue2180_AnAgentMakesBindsAndSharesAFormThroughMCPAlone(t *testing.T) {
	agent := connect(t)
	form, table := bindForm(t, agent, "acc-2180-agent", formFields, map[string]any{"auth_mode": "public"})
	share := shareFormsAsset(t, agent, saveFormsAsset(t, agent, form))
	token := "agent-" + endpointName("t")
	formsBrowser(t, "share", "FORMS_SHARE_PATH="+share+"?t="+url.QueryEscape(token), "FORMS_COUNT=1", "FORMS_TOKEN="+token)
	rows := formRows(t, agent, table, 1)
	if v := rows[0].values(); v["email"] != "anon0@example.com" || v["t"] != token {
		t.Fatalf("the agent's form stored %v", v)
	}
}
