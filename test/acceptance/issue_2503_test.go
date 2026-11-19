//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2503: a Verified badge marks an asset someone with authority has
// checked. Administrators verify, and so do the people of a persona carrying
// the portal permission verify_assets ("Can verify assets"); an owner without
// it cannot verify their own asset. A verification records who, when, the
// version, a note and an optional expiry; a newer version reads "changed
// since verified"; the badge is removed when the expiry passes, the owner
// being reminded once before; search ranks a verified asset above an
// unverified one at equal relevance. The badge grants nothing.
//
// Wire forms: manage_asset's `action`, `asset_id`, `note` and `expires_at` are
// typed string, sent as strings; `expires_at` is sent as an RFC 3339 time and
// as a date, the two forms its description admits. The portal routes take a
// JSON object body {note, expires_at}. A manage_asset update carrying a
// `verification` object is sent to show the schema refuses it.

// verifierPersona creates a persona whose portal block carries verify_assets,
// a key whose role resolves to it, and returns that caller and the persona.
func verifierPersona(t *testing.T, admin *client) (*client, string) {
	t.Helper()
	name := fmt.Sprintf("acc-2503-verifier-%d", time.Now().UnixNano())
	role := strings.ReplaceAll(name, "-", "_")
	body := map[string]any{
		"name": name, "display_name": "Acceptance 2503 verifier", "roles": []string{role},
		"allow_tools": []string{"*"}, "allow_connections": []string{"*"},
		"portal": map[string]any{"verify_assets": true},
	}
	if code := admin.restJSON(http.MethodPost, "/api/v1/admin/personas", body); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("creating persona %s: HTTP %d", name, code)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/personas/"+name, nil) })
	status, saved := admin.rest(http.MethodGet, "/api/v1/admin/personas/"+name, nil)
	portal, _ := saved["portal"].(map[string]any)
	if status != http.StatusOK || portal["verify_assets"] != true {
		t.Fatalf("the persona editor's Can verify assets was not stored: HTTP %d %v", status, saved["portal"])
	}
	status, key := admin.rest(http.MethodPost, "/api/v1/admin/auth/keys", jsonBody(t, map[string]any{
		"name": name, "roles": []string{role}, "expires_in": "1h",
	}))
	if status != http.StatusCreated {
		t.Fatalf("minting a key for %s: %d %v", name, status, key)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/auth/keys/"+name, nil) })
	return connectAs(t, fmt.Sprint(key["key"])), name
}

// verificationOf is an asset's verification object, nil when it has none.
func verificationOf(asset map[string]any) map[string]any {
	v, _ := asset["verification"].(map[string]any)
	return v
}

// TestIssue2503_AHolderVerifiesOnBothSurfaces: a member of a granted persona
// verifies an asset with a note and an expiry through the portal and through
// manage_asset; the badge reads who, the note and the expiry.
func TestIssue2503_AHolderVerifiesOnBothSurfaces(t *testing.T) {
	owner := connect(t)
	verifier, persona := verifierPersona(t, owner)
	s := stamp2502()
	viaPortal := saveAsset2502(t, owner, "acc-2503-portal-"+s, "text/markdown", "# Ledger\n", nil)
	viaTool := saveAsset2502(t, owner, "acc-2503-tool-"+s, "text/markdown", "# Ledger\n", nil)
	for _, id := range []string{viaPortal, viaTool} {
		// The verifier must be able to open what they verify: the assets are
		// shared with their persona.
		if status, body := owner.rest(http.MethodPost, "/api/v1/portal/assets/"+id+"/shares", jsonBody(t, map[string]any{
			"shared_with_persona": persona, "permission": "viewer",
		})); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("sharing %s with persona %s: HTTP %d %v", id, persona, status, body)
		}
	}

	expires := time.Now().Add(60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	status, body := verifier.rest(http.MethodPut, "/api/v1/portal/assets/"+viaPortal+"/verification",
		jsonBody(t, map[string]any{"note": "Checked against the ledger", "expires_at": expires}))
	if status != http.StatusOK {
		t.Fatalf("PUT verification as a holder: HTTP %d %v", status, body)
	}
	v := verificationOf(body)
	if v == nil || v["note"] != "Checked against the ledger" || v["expires_at"] == nil || v["changed_since_verified"] != false {
		t.Fatalf("the portal verification reads %v", body["verification"])
	}

	for form, at := range map[string]string{"rfc3339": expires, "date": time.Now().Add(90 * 24 * time.Hour).UTC().Format(time.DateOnly)} {
		out := verifier.call("manage_asset", map[string]any{
			"action": "verify", "asset_id": viaTool, "note": "Agent check " + form, "expires_at": at,
		})
		if v := verificationOf(out); v == nil || v["note"] != "Agent check "+form || v["expires_at"] == nil {
			t.Fatalf("manage_asset verify (%s) reads %v", form, out["verification"])
		}
	}
	got := owner.call("manage_asset", map[string]any{"action": "get", "asset_id": viaTool})
	if v := verificationOf(got); v == nil || v["verified_by"] == "" {
		t.Fatalf("manage_asset get carries no verification: %v", got["verification"])
	}
}

// TestIssue2503_TheOwnerWithoutThePermissionIsRefused: the owner without the
// grant is refused on both surfaces, and a manage_asset update or a portal
// update carrying verification fields leaves the asset unverified.
func TestIssue2503_TheOwnerWithoutThePermissionIsRefused(t *testing.T) {
	owner := connect(t)
	author := connectAs(t, engineerKey)
	id := saveAsset2502(t, author, "acc-2503-own-"+stamp2502(), "text/markdown", "# Mine\n", nil)

	if status, body := author.rest(http.MethodPut, "/api/v1/portal/assets/"+id+"/verification",
		jsonBody(t, map[string]any{"note": "I checked it myself"})); status != http.StatusForbidden {
		t.Fatalf("the owner's PUT verification answered HTTP %d %v, want 403", status, body)
	}
	res, text, err := author.callRaw("manage_asset", map[string]any{"action": "verify", "asset_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text, "Can verify assets") {
		t.Fatalf("the owner's manage_asset verify was not refused naming the permission: %s", text)
	}
	res, text, err = author.callRaw("manage_asset", map[string]any{
		"action": "update", "asset_id": id, "name": "acc-2503-own-renamed",
		"verification": map[string]any{"verified_by": "nobody@example.com", "verified_version": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Logf("an update carrying a verification object was accepted (%s); its fields must be ignored", text)
	}
	author.rest(http.MethodPut, "/api/v1/portal/assets/"+id, jsonBody(t, map[string]any{
		"name": "acc-2503-own-portal", "verification": map[string]any{"verified_by": "nobody@example.com"},
	}))
	_, asset := portalAsset2502(t, owner, id)
	if v := verificationOf(asset); v != nil {
		t.Fatalf("the owner forged a verification through an update: %v", v)
	}
}

// TestIssue2503_ANewVersionReadsChangedSinceVerified: a new version of a
// verified asset keeps the badge and reads changed since verified.
func TestIssue2503_ANewVersionReadsChangedSinceVerified(t *testing.T) {
	admin := connect(t)
	id := saveAsset2502(t, admin, "acc-2503-change-"+stamp2502(), "text/markdown", "# v1\n", nil)
	admin.call("manage_asset", map[string]any{"action": "verify", "asset_id": id, "note": "v1 checked"})
	admin.call("manage_asset", map[string]any{"action": "update", "asset_id": id, "content": "# v2\n"})
	_, asset := portalAsset2502(t, admin, id)
	v := verificationOf(asset)
	if v == nil || v["changed_since_verified"] != true || v["verified_version"] != float64(1) {
		t.Fatalf("after a new version the badge reads %v; want verified_version 1, changed_since_verified true", v)
	}
}

// TestIssue2503_TheBadgeIsRemovedAtItsExpiryAfterOneReminder: the owner is
// reminded once before the expiry, and a verification whose expiry passes
// reads as none.
func TestIssue2503_TheBadgeIsRemovedAtItsExpiryAfterOneReminder(t *testing.T) {
	// The reminder pass runs each minute and this waits on two of its passes
	// and an expiry, longer than a default session lasts.
	admin := connectFor(t, 10*time.Minute)
	owner := connectAtFor(t, baseURL(), analystKey, 10*time.Minute)
	s := stamp2502()
	soonName, laterName := "acc-2503-soon-"+s, "acc-2503-later-"+s
	soon := saveAsset2502(t, owner, soonName, "text/markdown", "# soon\n", nil)
	later := saveAsset2502(t, owner, laterName, "text/markdown", "# later\n", nil)
	for _, id := range []string{soon, later} {
		share2502(t, owner, id, "admin@example.com", "viewer")
	}

	// A verification expiring within the week is reminded on the reminder
	// pass's next run (each minute): one notification to the owner.
	admin.call("manage_asset", map[string]any{
		"action": "verify", "asset_id": soon, "expires_at": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
	})
	reminders := func(name string) int {
		status, page := admin.rest(http.MethodGet, "/api/v1/admin/notifications?limit=200&category=verification", nil)
		if status != http.StatusOK {
			t.Fatalf("GET notifications: HTTP %d %v", status, page)
		}
		n := 0
		rows, _ := page["data"].([]any)
		for _, r := range rows {
			row, _ := r.(map[string]any)
			if row["item_title"] == name && row["recipient"] == analystEmail {
				n++
			}
		}
		return n
	}
	poll2503(t, 3*time.Minute, "the owner's reminder of the verification of "+soon, func() bool { return reminders(soonName) > 0 })

	// A second asset verified after the first reminder is reminded on a later
	// pass; when it has been, the first asset still has its one reminder.
	admin.call("manage_asset", map[string]any{
		"action": "verify", "asset_id": later, "expires_at": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
	})
	poll2503(t, 3*time.Minute, "the reminder of the verification of "+later, func() bool { return reminders(laterName) > 0 })
	if n := reminders(soonName); n != 1 {
		t.Fatalf("the verification of %s was reminded %d times; want once", soon, n)
	}

	// An expiry a few seconds out: the badge reads, then is gone once it passes.
	admin.call("manage_asset", map[string]any{
		"action": "verify", "asset_id": later, "expires_at": time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339),
	})
	poll2503(t, time.Minute, "the badge of "+later+" to be removed at its expiry", func() bool {
		_, asset := portalAsset2502(t, admin, later)
		return verificationOf(asset) == nil
	})
}

// TestIssue2503_SearchRanksTheVerifiedAssetFirst: a verified and an
// unverified asset with the same text: search returns the verified one first,
// in the portal and through manage_asset.
func TestIssue2503_SearchRanksTheVerifiedAssetFirst(t *testing.T) {
	admin := connect(t)
	s := stamp2502()
	word := "vq2503" + s[len(s)-6:]
	name := "acc-2503-rank-" + word
	plain := saveAsset2502(t, admin, name, "text/markdown", "# "+word+" quarterly ledger\n", nil)
	verified := saveAsset2502(t, admin, name, "text/markdown", "# "+word+" quarterly ledger\n", nil)
	admin.call("manage_asset", map[string]any{"action": "verify", "asset_id": verified})

	status, page := admin.rest(http.MethodGet, "/api/v1/portal/assets/search?scope=mine&q="+word, nil)
	if status != http.StatusOK {
		t.Fatalf("search: HTTP %d %v", status, page)
	}
	rows, _ := page["data"].([]any)
	order := []string{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		asset, _ := row["asset"].(map[string]any)
		order = append(order, fmt.Sprint(asset["id"]))
	}
	if len(order) < 2 || order[0] != verified || !strings.Contains(strings.Join(order, ","), plain) {
		t.Fatalf("portal search order %v; want the verified %s first, then %s", order, verified, plain)
	}

	out := admin.call("manage_asset", map[string]any{"action": "search", "query": word})
	text := fmt.Sprint(out)
	if i, j := strings.Index(text, verified), strings.Index(text, plain); i < 0 || j < 0 || i > j {
		t.Fatalf("manage_asset search lists the unverified asset first: %s", text)
	}
}

// poll2503 polls check every two seconds until it holds, failing after within
// with what it waited for: the reminder pass and the expiry are background
// work, so a criterion on them waits to a bound rather than looking once.
func poll2503(t *testing.T, within time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s did not happen within %v", what, within)
}
