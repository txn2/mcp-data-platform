//go:build integration

package acceptance

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2531: an internal viewer of an asset another person owns
// reads its owner; an asset with versions by three people lists the two who
// are not the owner as contributors with their counts; an internal viewer
// reads whom it is shared with (personas, people, links) with no share token
// or link address anywhere in the response, as written, URL-escaped,
// JSON-escaped or base64; and an external viewer reads the owner and the
// contributors and is refused the share summary.
//
// Wire forms: the people and share-summary routes take the asset in their
// path and no body; the share route takes one JSON object of strings; the
// content route takes the raw content. No parameter admits a second form.

const assets2531 = "/api/v1/portal/assets/"

type world2531 struct {
	admin, owner *client
	asset        string
	internal     externalSession
	external     externalSession
	editor       externalSession
	tokens       []string
}

func newWorld2531(t *testing.T) world2531 {
	t.Helper()
	w := world2531{admin: connect(t), owner: connectAs(t, devOwnerAPIKey)}
	team := newPersona2290(t, w.admin, "2531", nil)
	client := newPersona2290(t, w.admin, "2531-client", map[string]any{"external": true})
	w.internal = newPerson2290(t, w.admin, team, "2531i")
	w.external = newPerson2290(t, w.admin, client, "2531x")
	w.editor = newPerson2290(t, w.admin, team, "2531e")

	saved := w.owner.call("save_asset", map[string]any{
		"name": fmt.Sprintf("acc-2531 report %d", time.Now().UnixNano()), "content_type": "text/html",
		"content": "<p>acc-2531 v1</p>", "description": "Acceptance #2531.",
	})
	w.asset, _ = saved["asset_id"].(string)
	if w.asset == "" {
		t.Fatalf("saving the asset: %v", saved)
	}
	t.Cleanup(func() {
		_, _, _ = w.owner.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": w.asset})
	})

	for _, share := range []map[string]any{
		{"shared_with_persona": team.name, "permission": "viewer"},
		{"shared_with_persona": client.name, "permission": "viewer"},
		{"shared_with_email": w.editor.email, "permission": "editor", "notify": false},
		{"access_mode": "public", "expires_in": "24h"},
	} {
		if status, out := w.admin.rest(http.MethodPost, assets2531+w.asset+"/shares", jsonBody(t, share)); status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("sharing the asset %v: %d %v", share, status, out)
		}
	}
	// The tokens the owner reads in Share, which the summary must not carry.
	req, _ := http.NewRequestWithContext(w.owner.ctx, http.MethodGet, w.owner.base+assets2531+w.asset+"/shares", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+w.owner.apiKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	var shares []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&shares); err != nil || len(shares) < 4 {
		t.Fatalf("reading the shares as the owner: %v %v", err, shares)
	}
	for _, s := range shares {
		if tok, _ := s["token"].(string); tok != "" {
			w.tokens = append(w.tokens, tok)
		}
	}
	return w
}

// version writes a new version of the asset as c.
func (w world2531) version(t *testing.T, c *client, content string) {
	t.Helper()
	if status, out := c.rest(http.MethodPut, assets2531+w.asset+"/content", strings.NewReader(content)); status != http.StatusOK {
		t.Fatalf("writing a version as %s: %d %v", c.apiKey[:8], status, out)
	}
}

func TestIssue2531_OwnerAndContributorsForEveryReader(t *testing.T) {
	w := newWorld2531(t)
	for _, v := range []string{"<p>acc-2531 v2</p>", "<p>acc-2531 v3</p>"} {
		if status, out := w.editor.do(t, http.MethodPut, assets2531+w.asset+"/content", v); status != http.StatusOK {
			t.Fatalf("the editor writing a version: %d %v", status, out)
		}
	}
	w.version(t, w.admin, "<p>acc-2531 v4</p>")

	for _, reader := range []externalSession{w.internal, w.external} {
		status, people := reader.do(t, http.MethodGet, assets2531+w.asset+"/people", nil)
		if status != http.StatusOK {
			t.Fatalf("%s reading who is behind the asset: %d %v", reader.email, status, people)
		}
		if owner, _ := people["owner"].(map[string]any); owner["email"] != "asset.owner@example.com" {
			t.Errorf("%s reads the owner as %v", reader.email, people["owner"])
		}
		got := map[string]float64{}
		for _, c := range asList(people["contributors"]) {
			n, _ := c["versions"].(float64)
			got[fmt.Sprint(c["email"])] = n
		}
		want := map[string]float64{strings.ToLower(w.editor.email): 2, "admin@example.com": 1}
		for k, v := range got {
			delete(got, k)
			got[strings.ToLower(k)] = v
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s reads contributors %v, want %v", reader.email, got, want)
		}
	}
}

func TestIssue2531_TheShareSummaryIsInternalAndCarriesNoToken(t *testing.T) {
	w := newWorld2531(t)
	status, summary := w.internal.do(t, http.MethodGet, assets2531+w.asset+"/share-summary", nil)
	if status != http.StatusOK {
		t.Fatalf("the internal viewer's share summary answered %d %v", status, summary)
	}
	if n, _ := summary["total"].(float64); n != 4 {
		t.Errorf("the summary counts %v shares, want 4: %v", summary["total"], summary)
	}
	if len(asList(summary["personas"])) != 2 || len(asList(summary["people"])) != 1 || len(asList(summary["links"])) != 1 {
		t.Errorf("the summary does not hold two personas, one person and one link: %v", summary)
	}
	raw, _ := json.Marshal(summary)
	body := string(raw)
	if len(w.tokens) == 0 {
		t.Fatal("the owner's shares carried no token to search for")
	}
	for _, tok := range w.tokens {
		escaped, _ := json.Marshal(tok)
		for _, form := range []string{
			tok, url.QueryEscape(tok), strings.Trim(string(escaped), `"`),
			base64.StdEncoding.EncodeToString([]byte(tok)), base64.RawURLEncoding.EncodeToString([]byte(tok)),
		} {
			if strings.Contains(body, form) {
				t.Errorf("the share summary carries a share token (%q)", form)
			}
		}
	}
	if strings.Contains(body, "/share/") || strings.Contains(body, `"token"`) {
		t.Errorf("the share summary carries a link address or a token field: %s", body)
	}

	if status, out := w.external.do(t, http.MethodGet, assets2531+w.asset+"/share-summary", nil); status != http.StatusForbidden {
		t.Errorf("an external viewer's share summary answered %d %v", status, out)
	}
	stranger := connectAs(t, "acme-peer-key")
	if status, _ := stranger.rest(http.MethodGet, assets2531+w.asset+"/share-summary", http.NoBody); status != http.StatusForbidden {
		t.Errorf("a person the asset is not shared with was answered %d", status)
	}
}
