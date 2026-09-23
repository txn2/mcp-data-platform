//go:build integration

package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1848: an application running scripts for its users could not find a
// run's outputs again. platform.export stamped fixed tags, the asset listing
// filtered on one tag, a caller who ran somebody else's script had no listing
// of what its runs wrote, and content was reached through share links rather
// than a short-lived URL a browser can be handed.
//
// What these hold, against the running platform: a script calling
// platform.export("x", rows, format="csv", tags=["report:sales"],
// metadata={"region": "west"}) produces an asset carrying those tags and that
// metadata, and the asset listing filtered on both finds it; a granted,
// non-admin key lists the outputs of its own runs; a signed URL downloads the
// exact version until it expires and answers 403 afterwards.
//
// Wire forms: platform.export's `tags` is a list of strings and `metadata` a
// dict, sent from Starlark; the listing's `tag` is a repeated query parameter
// and `metadata.<key>` a query parameter; the content-url route's `ttl` and
// `version` are integers in the query; the run route's body is an object.

// source1848 exports one CSV with tags and metadata.
const source1848 = `platform.export("x", [{"n": 1}, {"n": 2}], format="csv",
    tags=["report:sales"], metadata={"region": "west"})
`

func save1848(t *testing.T, c *client) (name, id string) {
	t.Helper()
	name = fmt.Sprintf("acc-1848-%d", time.Now().UnixNano())
	c.call("manage_script", map[string]any{
		"command": "create", "name": name, "source": source1848,
		"description": "Acceptance #1848: tags and metadata on an output.",
	})
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_script", map[string]any{"command": "delete", "name": name}) })
	got := c.call("manage_script", map[string]any{"command": "get", "name": name})
	id, _ = got["id"].(string)
	return name, id
}

// outputAsset runs the script as c over the run route and returns the asset
// id and version of its one output.
func outputAsset(t *testing.T, c *client, scriptID string) (assetID string, version int) {
	t.Helper()
	status, run := c.rest(http.MethodPost, "/api/v1/portal/scripts/"+scriptID+"/runs?wait=60", jsonBody(t, map[string]any{"params": map[string]any{}}))
	if status != http.StatusOK || run["status"] != "succeeded" {
		t.Fatalf("the run answered %d %v", status, run)
	}
	outputs, _ := run["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("the run wrote %v; want one output", run["outputs"])
	}
	out, _ := outputs[0].(map[string]any)
	assetID, _ = out["asset_id"].(string)
	v, _ := out["asset_version"].(float64)
	return assetID, int(v)
}

func TestIssue1848_AnOutputCarriesItsTagsAndMetadataAndIsFoundByThem(t *testing.T) {
	c := connect(t)
	_, id := save1848(t, c)
	assetID, _ := outputAsset(t, c, id)

	status, asset := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID, http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(asset["tags"]), "report:sales") {
		t.Errorf("the asset (%d) carries tags %v; want report:sales", status, asset["tags"])
	}
	status, versions := c.rest(http.MethodGet, "/api/v1/portal/assets/"+assetID+"/versions", http.NoBody)
	if status != http.StatusOK || !strings.Contains(fmt.Sprint(versions), "region:west") {
		t.Errorf("the version (%d) carries no region=west metadata: %v", status, versions)
	}
	if !strings.Contains(fmt.Sprint(versions), "run_id:") {
		t.Errorf("the version does not record its run: %v", versions)
	}

	found := c.list("/api/v1/portal/assets?tag=report:sales&tag=script&metadata.region=west&limit=100")
	if !strings.Contains(fmt.Sprint(found), assetID) {
		t.Errorf("the listing filtered on both tags and the metadata does not find %s", assetID)
	}
	miss := c.list("/api/v1/portal/assets?tag=report:sales&metadata.region=east&limit=100")
	if strings.Contains(fmt.Sprint(miss), assetID) {
		t.Errorf("metadata.region=east found the west output")
	}
}

func TestIssue1848_AGranteeListsTheOutputsOfItsOwnRuns(t *testing.T) {
	admin := connect(t)
	_, id := save1848(t, admin)
	keyName, key := issueKey1846(t, admin, nil)
	if status, body := admin.rest(http.MethodPost, "/api/v1/portal/scripts/"+id+"/grants",
		jsonBody(t, map[string]any{"principal_kind": "api_key", "principal": keyName})); status != http.StatusCreated {
		t.Fatalf("granting answered %d %v", status, body)
	}
	app := &client{t: t, ctx: admin.ctx, apiKey: key, base: admin.base}
	assetID, _ := outputAsset(t, app, id)

	status, listed := app.rest(http.MethodGet, "/api/v1/portal/scripts/runs/outputs?tag=report:sales&metadata.region=west", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("the grantee's outputs listing answered %d %v", status, listed)
	}
	rows, _ := listed["data"].([]any)
	var link string
	for _, r := range rows {
		row, _ := r.(map[string]any)
		out, _ := row["output"].(map[string]any)
		if out["asset_id"] == assetID {
			link, _ = row["content_url"].(string)
		}
	}
	if link == "" {
		t.Fatalf("the grantee's outputs do not list %s with a link: %v", assetID, listed)
	}
	if body := getAnonymously(t, admin.base, link); !strings.Contains(body, "n") {
		t.Errorf("the output's link downloaded %q", body)
	}
}

func TestIssue1848_ASignedURLServesTheVersionUntilItExpires(t *testing.T) {
	c := connect(t)
	_, id := save1848(t, c)
	assetID, version := outputAsset(t, c, id)

	status, minted := c.rest(http.MethodGet, fmt.Sprintf("/api/v1/portal/assets/%s/content-url?ttl=2&version=%d", assetID, version), http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("minting answered %d %v", status, minted)
	}
	path, _ := minted["path"].(string)
	if body := getAnonymously(t, c.base, path); !strings.Contains(body, "n\n1\n2") {
		t.Errorf("the signed URL downloaded %q; want the CSV version", body)
	}
	time.Sleep(3 * time.Second)
	res, err := http.Get(c.base + path) //nolint:noctx // a bare GET is the point: no session
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("the expired URL answered %d; want 403", res.StatusCode)
	}
}

// getAnonymously fetches a signed link with no credentials, on the process
// under test: an absolute link names the deployment's public base URL, which
// on the dev stack is the UI's port rather than this one.
func getAnonymously(t *testing.T, base, link string) string {
	t.Helper()
	target := link
	if i := strings.Index(link, "/api/v1/portal/content/"); i >= 0 {
		target = base + link[i:]
	}
	res, err := http.Get(target) //nolint:noctx // a bare GET is the point: no session
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s answered %d %s", target, res.StatusCode, raw)
	}
	return string(raw)
}
