//go:build integration

package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The dev-stack smoke (#2248): one call through the platform's own surfaces
// per service `make dev` starts, run by `make dev-smoke` in under a minute and
// by `make preverify` when the diff changes dev/. A change to the stack had no
// check short of the whole suite: the #2245 store over TLS passed the aws
// CLI, which speaks HTTP/1.1, and refused the platform's HTTP/2, failing 368
// criteria in a 45-minute run.
//
// Each check runs in parallel with the others and waits at most smokeWait for
// work the platform does in the background (an embedding, a tile).

const smokeWait = 45 * time.Second

func smokeStamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// smokeWord is a word no other document holds: the clock in base 26, letters
// only, so a search on it is not matched against the digits other test
// documents are named with.
func smokeWord() string {
	n := time.Now().UnixNano()
	var b strings.Builder
	for n > 0 {
		b.WriteByte(byte('a' + n%26))
		n /= 26
	}
	return "smoke" + b.String()
}

// smokeUntil polls done until it reports true or smokeWait passes.
func smokeUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(smokeWait)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, smokeWait)
		}
		time.Sleep(time.Second)
	}
}

// The managed-resource store is the SeaweedFS served over TLS: a file written
// through the platform reads back with the digest it was written with.
func TestDevSmoke_AResourceReadsBackFromTheTLSStore(t *testing.T) {
	t.Parallel()
	c := connect(t)
	stamp := smokeStamp()
	content := "smoke,value\n" + stamp + ",1\n"
	created := c.call("manage_resource", map[string]any{
		"action":       "create",
		"display_name": "Dev smoke " + stamp,
		"description":  "Dev smoke: a file written to the TLS store and read back.",
		"filename":     "dev-smoke-" + stamp + ".csv",
		"path":         "acceptance/dev-smoke",
		"content_type": "text/csv",
		"content":      content,
	})
	id, _ := created["resource_id"].(string)
	if id == "" {
		t.Fatalf("manage_resource create returned no resource_id: %v", created)
	}
	t.Cleanup(func() {
		if _, text, err := c.callRaw("manage_resource", map[string]any{"action": "delete", "resource_id": id}); err != nil {
			t.Logf("deleting resource %s: %v %s", id, err, text)
		}
	})
	sum := sha256.Sum256([]byte(content))
	if got, n := readContentDigest1631(t, c, id); got != hex.EncodeToString(sum[:]) || n != int64(len(content)) {
		t.Fatalf("resource %s read back %d bytes with sha256 %s, want %d bytes with %s", id, n, got, len(content), hex.EncodeToString(sum[:]))
	}
}

// The portal store is the plain-HTTP one: an asset saved through the platform
// reads back with its content.
func TestDevSmoke_AnAssetReadsBackFromThePortalStore(t *testing.T) {
	t.Parallel()
	c := connect(t)
	body := "<p>dev smoke " + smokeStamp() + "</p>"
	id := saveAsset1787(t, c, "text/html", body)
	status, raw := c.restText("/api/v1/portal/assets/" + id + "/content")
	if status != http.StatusOK || raw != body {
		t.Fatalf("asset %s content: status %d %q, want 200 %q", id, status, raw, body)
	}
}

// Each scratch catalog answers a query through the platform's Trino toolkit.
func TestDevSmoke_EachScratchCatalogAnswersAQuery(t *testing.T) {
	t.Parallel()
	c := connect(t)
	for _, conn := range []string{scratchConnection, scratchResourceConnection} {
		out := c.call("trino_query", map[string]any{
			"connection": conn,
			"sql":        "SELECT count(*) AS n FROM information_schema.schemata",
			"purpose":    "Dev smoke: the scratch catalog answers.",
		})
		if rows, _ := out["rows"].([]any); len(rows) != 1 {
			t.Fatalf("trino_query on %s returned %v, want one row", conn, out)
		}
	}
}

// A person signs in through Keycloak and the platform accepts the token.
func TestDevSmoke_APersonSignsInThroughKeycloak(t *testing.T) {
	t.Parallel()
	token := issue1759SignIn(t, issue1759AnalystEmail, issue1759AnalystPass)
	status, me := issue1759Request(t, token, http.MethodGet, issue1759MePath, nil)
	if status != http.StatusOK || !strings.EqualFold(fmt.Sprint(me["email"]), issue1759AnalystEmail) {
		t.Fatalf("GET %s with the Keycloak token: status %d %v, want 200 as %s", issue1759MePath, status, me, issue1759AnalystEmail)
	}
}

// An asset just saved is embedded (Ollama) and found by search on its words.
func TestDevSmoke_SearchFindsAJustEmbeddedAsset(t *testing.T) {
	t.Parallel()
	c := connect(t)
	words := smokeWord()
	saved := c.call("save_asset", map[string]any{
		"name":         words,
		"content":      "# " + words + "\n\nA document the dev smoke searches for.",
		"content_type": "text/markdown",
		"description":  "Dev smoke: a document the embedding service indexes.",
	})
	id, _ := saved["asset_id"].(string)
	if id == "" {
		t.Fatalf("save_asset returned no asset_id: %v", saved)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("manage_asset", map[string]any{"action": "delete", "asset_id": id}) })
	reference := "mcp:asset:" + id
	var last map[string]any
	smokeUntil(t, "search finds "+reference, func() bool {
		last = c.call("search", map[string]any{
			"intent": words, "sources": []any{"assets"}, "limit": 25,
			"purpose": "Dev smoke: the embedding service indexed the asset.",
		})
		if searchHolds1551(last, reference) {
			return true
		}
		t.Logf("search for %q does not hold %s yet: %v", words, reference, last)
		return false
	})
}

// The renderer draws a tile for an asset just saved.
func TestDevSmoke_TheRendererDrawsATile(t *testing.T) {
	t.Parallel()
	c := connect(t)
	id := saveAsset1787(t, c, "text/html", "<h1>dev smoke "+smokeStamp()+"</h1>")
	smokeUntil(t, "a tile for asset "+id, func() bool {
		row := assetRow1787(t, c, id)
		if failure, _ := row["thumbnail_failure"].(string); failure != "" {
			t.Fatalf("the platform could not draw asset %s: %s", id, failure)
		}
		return intField1787(row, "thumbnail_version") >= intField1787(row, "current_version")
	})
}

// The CalDAV server answers through a caldav connection: a calendar made
// for the check is listed by calendar_list.
func TestDevSmoke_TheCalDAVServerListsACalendar(t *testing.T) {
	t.Parallel()
	requireCalDAV(t)
	c := connect(t)
	href := issue2246Calendar(t, "Smoke "+smokeStamp())
	name := issue2246Connect(t, c)
	out := c.call("calendar_list", map[string]any{"connection": name, "purpose": "Dev smoke: the CalDAV server answers."})
	if !strings.Contains(fmt.Sprint(out["calendars"]), href) {
		t.Fatalf("calendar_list did not list %s: %v", href, out)
	}
}

// The IMAP server answers through an imap connection: a message appended for
// the check is found by mail_search.
func TestDevSmoke_TheIMAPServerFindsAMessage(t *testing.T) {
	t.Parallel()
	c := connect(t)
	folder := issue2247Folder(t)
	name := issue2247Connect(t, c, folder)
	out := c.call("mail_search", map[string]any{"connection": name, "folder": folder, "purpose": "Dev smoke: the IMAP server answers."})
	if msgs, _ := out["messages"].([]any); len(msgs) == 0 {
		t.Fatalf("mail_search found nothing in %s: %v", folder, out)
	}
}
