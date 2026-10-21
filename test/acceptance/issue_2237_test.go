//go:build integration

package acceptance

import (
	"net/http"
	"strings"
	"testing"
)

// Acceptance for #2237: a Google API refusing a call a service-account
// connection made carries the operator's next step, chosen by the refusal's
// status and reasons rather than by one product's message text. Google answers
// every API in one error shape, so a 401 UNAUTHENTICATED with any message in
// that shape is read as an account without access in the product.
//
// The connection is #2061's: a Google key file whose grant Keycloak answers,
// with a Google API as its upstream. Google refuses the Keycloak-issued token
// with a 401 UNAUTHENTICATED in its own shape, which carried no hint before
// this change: it is neither PERMISSION_DENIED nor the one product's text the
// hint used to match.
//
// Wire forms: google_service_account_json is sent as a string of JSON,
// oauth_scope as a space-delimited string and base_url as a string;
// api_invoke_endpoint takes method and path strings and no body.

const googleAPI2237 = "https://sheets.googleapis.com"

// TestIssue2237_AGoogle401CarriesTheAccessStep: a call through a Google
// service-account connection that Google refuses with 401 UNAUTHENTICATED
// answers with the hint to grant the account access inside the product.
func TestIssue2237_AGoogle401CarriesTheAccessStep(t *testing.T) {
	admin := connect(t)
	name := "acc-2237-" + unique1579()
	if status := connection2061(t, admin, name, grant2061(map[string]any{
		"google_service_account_json": keyFile2061(t),
		"base_url":                    googleAPI2237,
	})); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("saving the connection answered %d", status)
	}
	out := admin.call("api_invoke_endpoint", map[string]any{
		"connection": name, "method": http.MethodGet, "path": "/v4/spreadsheets/acceptance-2237",
		"purpose": "Acceptance #2237: what a Google refusal tells the operator.",
	})
	if status, _ := out["status"].(float64); status != http.StatusUnauthorized {
		t.Fatalf("Google answered %v, not 401: %v", out["status"], out)
	}
	body, _ := out["body"].(map[string]any)
	refusal, _ := body["error"].(map[string]any)
	if refusal["status"] != "UNAUTHENTICATED" {
		t.Fatalf("the refusal is not in Google's shape: %v", body)
	}
	if hint, _ := out["hint"].(string); !strings.Contains(hint, "grant the service account's email access inside the product") {
		t.Errorf("the hint is %q; want the access step", hint)
	}
}
