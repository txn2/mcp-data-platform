//go:build integration

package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2537: a person who signed in through an asset's public link
// (which gives them a viewer share of origin public_link_login) and was then
// shared the asset explicitly holds two active shares, and the share summary
// lists them once, with the higher permission, counted once in total.
//
// Wire forms: the share route takes one JSON object of strings; the
// share-summary route takes the asset in its path and no body; the public
// view takes the link's token in its path. No parameter admits a second form.

func TestIssue2537_APersonWithTwoSharesIsListedOnce(t *testing.T) {
	admin, owner := connect(t), connectAs(t, devOwnerAPIKey)
	team := newPersona2290(t, admin, "2537", nil)
	person := newPerson2290(t, admin, team, "2537p")
	reader := newPerson2290(t, admin, team, "2537r")

	saved := owner.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2537 report %d", time.Now().UnixNano()), "content_type": "text/html",
		"content": "<p>acc-2537</p>", "description": "Acceptance #2537.",
	})
	asset, _ := saved["asset_id"].(string)
	if asset == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": asset})
	})

	status, link := owner.rest(http.MethodPost, assets2531+asset+"/shares", jsonBody(t, map[string]any{"access_mode": "public", "expires_in": "24h"}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("making the public link: %d %v", status, link)
	}
	token := issue2537Token(t, link)

	// The person opens the public link signed in, which gives them a viewer
	// share of their own.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+"/portal/view/"+token, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	res, err := person.hc.Do(req)
	if err != nil {
		t.Fatalf("opening the public link signed in: %v", err)
	}
	_ = res.Body.Close()

	// Then the owner shares the asset with them explicitly, as an editor, and
	// with the reader as a viewer.
	for _, share := range []map[string]any{
		{"shared_with_email": person.email, "permission": "editor", "notify": false},
		{"shared_with_email": reader.email, "permission": "viewer", "notify": false},
	} {
		if status, out := owner.rest(http.MethodPost, assets2531+asset+"/shares", jsonBody(t, share)); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("sharing the asset %v: %d %v", share, status, out)
		}
	}
	held := issue2537ActiveSharesOf(t, owner, asset, person.email)
	if held < 2 {
		t.Fatalf("the person holds %d active shares; the public link did not give them one", held)
	}

	status, summary := reader.do(t, http.MethodGet, assets2531+asset+"/share-summary", nil)
	if status != http.StatusOK {
		t.Fatalf("the share summary answered %d %v", status, summary)
	}
	listed := 0
	for _, p := range asList(summary["people"]) {
		if strings.EqualFold(fmt.Sprint(p["email"]), person.email) {
			listed++
			if p["permission"] != "editor" {
				t.Errorf("the person is listed as %v, want the higher permission editor", p["permission"])
			}
		}
	}
	if listed != 1 {
		t.Errorf("the person holding %d shares is listed %d times: %v", held, listed, summary["people"])
	}
	if len(asList(summary["people"])) != 2 {
		t.Errorf("the summary lists %d people, want 2: %v", len(asList(summary["people"])), summary["people"])
	}
	if n, _ := summary["total"].(float64); n != 3 {
		t.Errorf("the summary counts %v, want 3 (two people and one link): %v", summary["total"], summary)
	}
}

// issue2537Token is the token of the share the share route answered with.
func issue2537Token(t *testing.T, share map[string]any) string {
	t.Helper()
	if tok, _ := share["token"].(string); tok != "" {
		return tok
	}
	if inner, ok := share["share"].(map[string]any); ok {
		if tok, _ := inner["token"].(string); tok != "" {
			return tok
		}
	}
	t.Fatalf("the share route answered no token: %v", share)
	return ""
}

// issue2537ActiveSharesOf counts the asset's unrevoked shares naming email, as
// its owner reads them.
func issue2537ActiveSharesOf(t *testing.T, owner *client, asset, email string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(owner.ctx, http.MethodGet, owner.base+assets2531+asset+"/shares", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+owner.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	var shares []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&shares); err != nil {
		t.Fatalf("reading the shares: %v", err)
	}
	n := 0
	for _, s := range shares {
		if strings.EqualFold(fmt.Sprint(s["shared_with_email"]), email) && s["revoked"] != true {
			n++
		}
	}
	return n
}
