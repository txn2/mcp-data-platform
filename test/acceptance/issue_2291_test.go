//go:build integration

package acceptance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2291: the Share dialog's three server changes. An item has one
// general link, created, changed in place keeping its token, and removed; a
// grant's permission changes in place, and an address with no account can
// only view; the personas a share can name carry how many people each reaches.
//
// Wire forms: the link body as a JSON object with access_mode as the strings
// "public" and "authenticated", expires_in as a string, notice_text as a
// string, as the empty string and as null, and hide_expiration as true, false
// and null; the permission body as a JSON object with permission as the
// strings "editor" and "viewer"; the share body as a JSON object with
// shared_with_email and permission strings and notify as the boolean false.

// issue2291Raw sends one request as c and answers the status and the raw body,
// for routes that answer an array or nothing.
func issue2291Raw(t *testing.T, c *client, method, path, body string) (int, []byte) {
	t.Helper()
	var r io.Reader = http.NoBody
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, r)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

// issue2291Shares is the asset's shares still in force.
func issue2291Shares(t *testing.T, c *client, assetID string) []map[string]any {
	t.Helper()
	status, raw := issue2291Raw(t, c, http.MethodGet, "/api/v1/portal/assets/"+assetID+"/shares", "")
	if status != http.StatusOK {
		t.Fatalf("listing the shares: %d %s", status, raw)
	}
	var all []map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("decoding the shares: %v: %s", err, raw)
	}
	var live []map[string]any
	for _, s := range all {
		if revoked, _ := s["revoked"].(bool); !revoked {
			live = append(live, s)
		}
	}
	return live
}

// issue2291Links is the asset's links addressed to nobody.
func issue2291Links(t *testing.T, c *client, assetID string) []map[string]any {
	t.Helper()
	var links []map[string]any
	for _, s := range issue2291Shares(t, c, assetID) {
		if s["shared_with_email"] == nil && s["shared_with_user_id"] == nil && s["shared_with_persona"] == nil && s["posted_to"] == nil {
			links = append(links, s)
		}
	}
	return links
}

// issue2291PutLink sets the asset's general link and answers the status and
// the link's share.
func issue2291PutLink(t *testing.T, c *client, assetID, body string) (int, map[string]any) {
	t.Helper()
	status, raw := issue2291Raw(t, c, http.MethodPut, "/api/v1/portal/assets/"+assetID+"/link", body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	share, _ := out["share"].(map[string]any)
	if share == nil {
		share = out
	}
	return status, share
}

// issue2291Page opens the shared page with no session, as a person handed the
// link does.
func issue2291Page(t *testing.T, c *client, token string) (int, string) {
	t.Helper()
	hc := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := hc.Get(c.base + "/portal/view/" + token)
	if err != nil {
		t.Fatalf("GET /portal/view/%s: %v", token, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, string(body)
}

// Choosing Anyone with the link makes one link with the default lifetime;
// Anyone who can sign in changes that same link, whose address is unchanged;
// the banner and the expiry notice change in place in every form their fields
// take; Only people added removes it; no choice makes a second link.
func TestIssue2291_GeneralAccessChangesTheOneLinkInPlace(t *testing.T) {
	c := connect(t)
	assetID := saveAsset1787(t, c, "text/markdown", "# Acceptance 2291\n\nThe general link.")

	status, link := issue2291PutLink(t, c, assetID, `{"access_mode":"public","expires_in":"24h"}`)
	if status != http.StatusCreated {
		t.Fatalf("making the link public: %d %v", status, link)
	}
	token, _ := link["token"].(string)
	if token == "" || link["expires_at"] == nil {
		t.Fatalf("the public link has no token or no expiry: %v", link)
	}
	if code, _ := issue2291Page(t, c, token); code != http.StatusOK {
		t.Fatalf("the public link answered %d with no session, want 200", code)
	}

	status, link = issue2291PutLink(t, c, assetID, `{"access_mode":"authenticated"}`)
	if status != http.StatusOK || link["token"] != token || link["access_mode"] != "authenticated" || link["expires_at"] != nil {
		t.Fatalf("changing the link to signed-in people: %d %v, want the same token %s, no expiry", status, link, token)
	}
	if code, _ := issue2291Page(t, c, token); code == http.StatusOK {
		t.Fatalf("a link for signed-in people opened with no session")
	}

	status, link = issue2291PutLink(t, c, assetID, `{"access_mode":"public","expires_in":"1h","notice_text":"Board only","hide_expiration":true}`)
	if status != http.StatusOK || link["token"] != token || link["notice_text"] != "Board only" || link["hide_expiration"] != true {
		t.Fatalf("setting the banner: %d %v", status, link)
	}
	if code, page := issue2291Page(t, c, token); code != http.StatusOK || !strings.Contains(page, "Board only") {
		t.Fatalf("the shared page (%d) does not show the new banner", code)
	}
	for _, body := range []string{
		`{"access_mode":"public","notice_text":null,"hide_expiration":null}`,
		`{"access_mode":"public"}`,
	} {
		status, link = issue2291PutLink(t, c, assetID, body)
		if status != http.StatusOK || link["notice_text"] != "Board only" || link["hide_expiration"] != true {
			t.Fatalf("%s did not keep the banner and the notice: %d %v", body, status, link)
		}
	}
	status, link = issue2291PutLink(t, c, assetID, `{"access_mode":"public","notice_text":"","hide_expiration":false}`)
	if status != http.StatusOK || link["notice_text"] != "" || link["hide_expiration"] == true {
		t.Fatalf("clearing the banner: %d %v", status, link)
	}

	if links := issue2291Links(t, c, assetID); len(links) != 1 || links[0]["token"] != token {
		t.Fatalf("after five choices the asset holds %d links (%v), want the one", len(links), links)
	}

	status, raw := issue2291Raw(t, c, http.MethodDelete, "/api/v1/portal/assets/"+assetID+"/link", "")
	if status != http.StatusNoContent {
		t.Fatalf("Only people added: %d %s", status, raw)
	}
	if links := issue2291Links(t, c, assetID); len(links) != 0 {
		t.Fatalf("after Only people added the asset still holds %v", links)
	}
	if code, _ := issue2291Page(t, c, token); code == http.StatusOK {
		t.Fatalf("the removed link still opens")
	}

	for _, body := range []string{`{"access_mode":"restricted"}`, `{"access_mode":"public"}`, `{"access_mode":"authenticated","expires_in":"1h"}`} {
		if status, out := issue2291PutLink(t, c, assetID, body); status != http.StatusBadRequest {
			t.Fatalf("%s answered %d %v, want 400", body, status, out)
		}
	}
}

// issue2291Member is a person the directory holds: they sign in through the
// identity provider, which is what records them.
const issue2291Member = issue1759AnalystEmail

// issue2291SignIn signs the member in through Keycloak and answers their
// bearer token; the sign-in is what puts them in the directory.
func issue2291SignIn(t *testing.T) string {
	t.Helper()
	token := issue1759SignIn(t, issue2291Member, issue1759AnalystPass)
	if status, me := issue1759Request(t, token, http.MethodGet, issue1759MePath, nil); status != http.StatusOK {
		t.Fatalf("GET %s as %s: %d %v", issue1759MePath, issue2291Member, status, me)
	}
	// The directory records a sign-in off the request path.
	admin := connect(t)
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, out := admin.rest(http.MethodGet, "/api/v1/portal/users?q="+issue2291Member, http.NoBody)
		users, _ := out["users"].([]any)
		for _, u := range users {
			if row, _ := u.(map[string]any); strings.EqualFold(fmt.Sprint(row["email"]), issue2291Member) {
				return token
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the directory does not hold %s after they signed in: %v", issue2291Member, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A grant's permission changes in place and the person opens it with the new
// one; a link has none to change; an address with no account can only view,
// at creation and on a change; a removal is undone by sharing again without
// an email.
func TestIssue2291_AGrantsPermissionChangesInPlace(t *testing.T) {
	c := connect(t)
	member := issue2291SignIn(t)
	assetID := saveAsset1787(t, c, "text/markdown", "# Acceptance 2291\n\nA grant.")

	status, out := c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": issue2291Member, "permission": "viewer", "notify": false}))
	if status != http.StatusCreated {
		t.Fatalf("sharing with %s: %d %v", issue2291Member, status, out)
	}
	share, _ := out["share"].(map[string]any)
	shareID, _ := share["id"].(string)

	status, out = c.rest(http.MethodPatch, "/api/v1/portal/shares/"+shareID, jsonBody(t, map[string]any{"permission": "editor"}))
	if status != http.StatusOK || out["permission"] != "editor" {
		t.Fatalf("Can view to Can edit: %d %v", status, out)
	}
	if got := issue2291SharedPermission(t, member, assetID); got != "editor" {
		t.Fatalf("%s opens the asset as %q after the change, want editor", issue2291Member, got)
	}
	status, out = c.rest(http.MethodPatch, "/api/v1/portal/shares/"+shareID, jsonBody(t, map[string]any{"permission": "viewer"}))
	if status != http.StatusOK || out["permission"] != "viewer" {
		t.Fatalf("Can edit to Can view: %d %v", status, out)
	}
	if n := len(issue2291Shares(t, c, assetID)); n != 1 {
		t.Fatalf("a permission change left %d shares, want the one", n)
	}

	guest := fmt.Sprintf("guest-%d@example.org", time.Now().UnixNano())
	status, out = c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": guest, "permission": "editor", "notify": false}))
	if status != http.StatusBadRequest {
		t.Fatalf("an editor grant to %s, who has no account: %d %v, want 400", guest, status, out)
	}
	status, out = c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": guest, "permission": "viewer", "notify": false}))
	if status != http.StatusCreated {
		t.Fatalf("a viewer grant to a guest: %d %v", status, out)
	}
	guestShare, _ := out["share"].(map[string]any)
	if status, out = c.rest(http.MethodPatch, "/api/v1/portal/shares/"+guestShare["id"].(string), jsonBody(t, map[string]any{"permission": "editor"})); status != http.StatusBadRequest {
		t.Fatalf("making a guest an editor: %d %v, want 400", status, out)
	}

	_, link := issue2291PutLink(t, c, assetID, `{"access_mode":"authenticated"}`)
	if status, out = c.rest(http.MethodPatch, "/api/v1/portal/shares/"+link["id"].(string), jsonBody(t, map[string]any{"permission": "editor"})); status != http.StatusBadRequest {
		t.Fatalf("an editor link: %d %v, want 400", status, out)
	}

	if status, out = c.rest(http.MethodDelete, "/api/v1/portal/shares/"+shareID, http.NoBody); status != http.StatusOK {
		t.Fatalf("Remove access: %d %v", status, out)
	}
	if got := issue2291SharedPermission(t, member, assetID); got != "" {
		t.Fatalf("%s still opens the asset after Remove access: %q", issue2291Member, got)
	}
	status, out = c.rest(http.MethodPost, "/api/v1/portal/assets/"+assetID+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": issue2291Member, "permission": "viewer", "notify": false}))
	if status != http.StatusCreated {
		t.Fatalf("Undo: %d %v", status, out)
	}
	if got := issue2291SharedPermission(t, member, assetID); got != "viewer" {
		t.Fatalf("after Undo %s opens the asset as %q, want viewer", issue2291Member, got)
	}
}

// issue2291SharedPermission is the permission the asset is shared with the
// member at, as their shared-with-me list says, or "".
func issue2291SharedPermission(t *testing.T, member, assetID string) string {
	t.Helper()
	status, out := issue1759Request(t, member, http.MethodGet, "/api/v1/portal/shared-with-me?limit=200", nil)
	if status != http.StatusOK {
		t.Fatalf("shared-with-me: %d %v", status, out)
	}
	rows, _ := out["data"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		asset, _ := row["asset"].(map[string]any)
		if asset["id"] == assetID {
			p, _ := row["permission"].(string)
			return p
		}
	}
	return ""
}

// The personas a share can name each say how many people they reach, counted
// as their members are notified.
func TestIssue2291_EachGroupSaysHowManyPeopleItReaches(t *testing.T) {
	c := connect(t)
	status, out := c.rest(http.MethodGet, "/api/v1/portal/share-personas", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("share-personas: %d %v", status, out)
	}
	personas, _ := out["personas"].([]any)
	if len(personas) == 0 {
		t.Fatalf("no personas: %v", out)
	}
	someone := false
	for _, p := range personas {
		persona, _ := p.(map[string]any)
		n, ok := persona["member_count"].(float64)
		if !ok {
			t.Fatalf("persona %v carries no member_count", persona)
		}
		someone = someone || n > 0
	}
	if !someone {
		t.Fatalf("no persona reaches anyone in a directory that holds people: %v", personas)
	}
}

// A file share's permission changes in place through the file's own route.
func TestIssue2291_AFileSharesPermissionChangesInPlace(t *testing.T) {
	c := connect(t)
	issue2291SignIn(t)
	id := issue2291SeedFile(t)

	status, out := c.rest(http.MethodPost, "/api/v1/resources/"+id+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": issue2291Member, "permission": "viewer", "notify": false}))
	if status != http.StatusCreated {
		t.Fatalf("sharing the file: %d %v", status, out)
	}
	shareID, _ := out["id"].(string)
	status, out = c.rest(http.MethodPatch, "/api/v1/resources/"+id+"/shares/"+shareID, jsonBody(t, map[string]any{"permission": "editor"}))
	if status != http.StatusOK || out["permission"] != "editor" {
		t.Fatalf("Can edit on a file: %d %v", status, out)
	}
	status, out = c.rest(http.MethodGet, "/api/v1/resources/"+id+"/shares", http.NoBody)
	shares, _ := out["shares"].([]any)
	if status != http.StatusOK || len(shares) != 1 || shares[0].(map[string]any)["permission"] != "editor" {
		t.Fatalf("the file's shares after the change: %d %v", status, out)
	}
}

// issue2291SeedFile writes one global file straight into the dev database and
// removes it when the test ends; a share names it and reads nothing of its
// content.
func issue2291SeedFile(t *testing.T) string {
	t.Helper()
	db, err := sql.Open("postgres", issue1694DevDSN())
	if err != nil {
		t.Fatalf("opening the dev database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	id := fmt.Sprintf("acc2291-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO resources (id, scope, path, filename, display_name, description, mime_type, size_bytes, s3_key, uri, uploader_sub)
		VALUES ($1, 'global', 'acceptance-2291', 'f.bin', 'Acceptance 2291', 'A file to share.', 'application/octet-stream', 1, $1, 'mcp://global/acceptance-2291/' || $1, 'acceptance')`, id); err != nil {
		t.Fatalf("seeding the file: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM resources WHERE id = $1`, id) })
	return id
}
