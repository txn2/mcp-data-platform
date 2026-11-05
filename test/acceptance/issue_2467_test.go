//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #2467: a space pasted before an api connection's Token URL failed the
// save as an unparseable URL. Leading and trailing whitespace is now removed
// from the URL, identifier, scope and duration fields on save, through the
// admin route the portal's connection form saves with; a secret is stored as
// it was sent.
//
// Wire forms: the route takes one JSON object; config's fields are strings,
// the one form each admits, sent with the whitespace a paste brings.

func TestIssue2467_PastedWhitespaceAroundAURLOrIDIsRemovedOnSave(t *testing.T) {
	admin := connect(t)
	name := fmt.Sprintf("acc-2467-%d", time.Now().UnixNano())
	path := "/api/v1/admin/connection-instances/api/" + name
	t.Cleanup(func() { admin.rest(http.MethodDelete, path, http.NoBody) })

	status, body := admin.rest(http.MethodPut, path, jsonBody(t, map[string]any{
		"config": map[string]any{
			"base_url":            "https://upstream.example.com\n",
			"auth_mode":           "oauth",
			"oauth_grant":         "client_credentials",
			"oauth_token_url":     " https://idp.example.com/oauth/token",
			"oauth_client_id":     "\tacceptance-2467 \r\n",
			"oauth_client_secret": "acceptance-2467-secret",
			"oauth_scope":         " read write\n",
			"call_timeout":        " 30s ",
		},
		"description": "Acceptance #2467: values pasted with whitespace.",
	}))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("the save answered %d, want it accepted: %v", status, body)
	}

	status, got := admin.rest(http.MethodGet, path, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the connection back answered %d: %v", status, got)
	}
	cfg, _ := got["config"].(map[string]any)
	for key, want := range map[string]string{
		"base_url":        "https://upstream.example.com",
		"oauth_token_url": "https://idp.example.com/oauth/token",
		"oauth_client_id": "acceptance-2467",
		"oauth_scope":     "read write",
		"call_timeout":    "30s",
	} {
		if cfg[key] != want {
			t.Errorf("%s reads back %q, want %q", key, cfg[key], want)
		}
	}
}
