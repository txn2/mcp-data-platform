//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Acceptance for #2362: a deployment with no ENCRYPTION_KEY refuses to store a
// webhook source's secret, as it refuses a form's sensitive fields. A replica
// started without the key is asked to create a source holding a secret and
// to create one keyed by the sender's public key.
//
// Wire forms: the admin route takes one JSON object.

func TestIssue2362_NoKeyNoStoredSecret(t *testing.T) {
	root, _ := filepath.Abs("../..")
	dir := filepath.Join(root, "build", "2362", "spool-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	r := issue2121Start(t, dir, issue2121NewStore(t), "ENCRYPTION_KEY=", "ENCRYPTION_KEY_PREVIOUS=")

	post := func(body map[string]any) (int, string) {
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, r.base+"/api/v1/admin/webhooks/sources", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", devAPIKey())
		res, err := http.DefaultClient.Do(req) // #nosec G704 -- the replica this test started
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close() //nolint:errcheck // read below
		text, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(text)
	}

	secret := issue1870Name("s2362-secret")
	status, text := post(map[string]any{
		"name": secret, "connection": issue1870Conn,
		"auth": map[string]any{"mode": "header_token", "header": "X-Hook", "secret": "acc2362-token"},
	})
	if status != http.StatusBadRequest || !strings.Contains(text, "ENCRYPTION_KEY") {
		t.Fatalf("creating a source holding a secret with no key answered %d %s, want 400 naming ENCRYPTION_KEY", status, text)
	}

	public := issue1870Name("s2362-ed25519")
	status, text = post(map[string]any{
		"name": public, "connection": issue1870Conn,
		"auth": map[string]any{"preset": "discord", "secret": strings.Repeat("ab", 32)},
	})
	if status != http.StatusCreated {
		t.Fatalf("creating a source keyed by a public key answered %d %s, want 201", status, text)
	}
	admin := connect(t)
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/webhooks/sources/"+public, http.NoBody) })
}
