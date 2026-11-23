//go:build integration

package acceptance

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #2721: a JSON spec is read as JSON, not through the loader's YAML
// parser that allocated two hundred times a large spec's size at startup. A
// spec in either form, its references included, still serves its operations
// through an api connection, and the platform's own admin API, the largest
// spec a deployment holds, still lists its operations.
//
// Wire forms: the spec is saved with PUT
// /api/v1/admin/api-catalogs/{id}/specs/{name} and a JSON object whose
// `content` is a string holding JSON in one case and YAML in the other, the
// two document forms the content admits; api_discover's `connection` and
// `purpose` and api_invoke_endpoint's `operation_id` are strings.
//
// The memory the first replica on a new version was killed for, about 3 GB
// within seconds, came from redrawing every site tile the release owed: a
// file a tile's page loaded was held whole, then base64 and a protocol
// message of it, all at once for every file the page asked for. A site whose
// page loads twelve large images (four larger than the file a drawn page is
// answered with, eight just under it) is published and drawn, then promoted,
// which draws the same release again for prod with no upload in flight; the
// replicas allocate less than six times the files the page loads while
// prod's tile is drawn (the bytes allocated, summed over the replicas, which
// unlike the heap in use do not depend on when each last collected its
// garbage), and the tile is drawn.
//
// Wire forms (tiles): the portal's site routes take JSON objects (name and
// description to create; environment, note, archive and source to publish;
// from and to to promote), sent as literal bodies; the archive is a managed
// resource uploaded as multipart/form-data. What each replica allocated is
// read from its /metrics as go_memstats_alloc_bytes_total.

const issue2721Purpose = "Acceptance #2721: a spec read as JSON serves its operations."

// issue2721Documents is one OpenAPI 3 document in each form, its response
// described through a component reference.
func issue2721Documents(op string) map[string]string {
	return map[string]string{
		"json": `{"openapi":"3.0.3","info":{"title":"api-test","version":"1"},"paths":{"/v1/whoami":{"get":{"operationId":"` + op +
			`","responses":{"200":{"description":"who","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Who"}}}}}}}},` +
			`"components":{"schemas":{"Who":{"type":"object","properties":{"key":{"type":"string"}}}}}}`,
		"yaml": "openapi: 3.0.3\ninfo:\n  title: api-test\n  version: '1'\npaths:\n  /v1/whoami:\n    get:\n      operationId: " + op +
			"\n      responses:\n        '200':\n          description: who\n          content:\n            application/json:\n" +
			"              schema:\n                $ref: '#/components/schemas/Who'\ncomponents:\n  schemas:\n    Who:\n      type: object\n" +
			"      properties:\n        key:\n          type: string\n",
	}
}

// TestIssue2721_ASpecInEitherFormServesItsOperations: each form is saved,
// served through a connection, listed by api_discover and called.
func TestIssue2721_ASpecInEitherFormServesItsOperations(t *testing.T) {
	c := connect(t)
	for form, document := range issue2721Documents("whoAmI2721") {
		t.Run(form, func(t *testing.T) {
			catalogID := fmt.Sprintf("acc-2721-%s-%d", form, time.Now().UnixNano())
			if code := c.restJSON(http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
				"id": catalogID, "name": catalogID, "display_name": "api-test (" + form + ")",
				"description": "The api-test fixture, #2721 acceptance.",
			}); code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("creating catalog %s: HTTP %d", catalogID, code)
			}
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/api-catalogs/"+catalogID, http.NoBody) })
			status, body := c.rest(http.MethodPut, "/api/v1/admin/api-catalogs/"+catalogID+"/specs/identity",
				jsonBody(t, map[string]any{"source_kind": "inline", "content": document}))
			if status != http.StatusOK && status != http.StatusNoContent {
				t.Fatalf("saving the %s spec: HTTP %d %v", form, status, body)
			}

			name := catalogID
			if code := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
				"config": map[string]any{
					"base_url": apiTestFixtureURL(), "auth_mode": "api_key", "credential": issue2005FixtureKey(),
					"api_key_placement": "header", "api_key_header": "X-API-Key",
					"connection_name": name, "catalog_id": catalogID,
					"connect_timeout": "5s", "call_timeout": "20s", "trust_level": "untrusted",
				},
				"description": "api-test fixture, #2721 acceptance.",
			}); code != http.StatusCreated && code != http.StatusOK {
				t.Fatalf("registering connection %s: HTTP %d", name, code)
			}
			t.Cleanup(func() { c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })
			issue1736AwaitConnection(t, c, name)

			ops := map[string]map[string]any{}
			collectOperations(c.call("api_discover", map[string]any{"connection": name, "purpose": issue2721Purpose}), ops)
			if _, ok := ops["whoAmI2721"]; !ok {
				t.Fatalf("api_discover did not list the %s spec's operation; it listed %v", form, keysOfOps(ops))
			}
			out := c.call("api_invoke_endpoint", map[string]any{"connection": name, "operation_id": "whoAmI2721", "purpose": issue2721Purpose})
			if code, _ := out["status"].(float64); code != http.StatusOK {
				t.Errorf("calling the operation answered %v, want 200: %v", out["status"], firstRunes(fmt.Sprint(out), 600))
			}
		})
	}
}

// TestIssue2721_ThePlatformsOwnAPIListsItsOperations: the platform-admin
// connection, served from the converted Swagger 2.0 document the platform
// embeds, lists the admin operations.
func TestIssue2721_ThePlatformsOwnAPIListsItsOperations(t *testing.T) {
	c := connect(t)
	ops := map[string]map[string]any{}
	collectOperations(c.call("api_discover", map[string]any{"connection": "platform-admin", "query": "list personas", "purpose": issue2721Purpose}), ops)
	if len(ops) == 0 {
		t.Fatal("api_discover on platform-admin listed no operation")
	}
}

const (
	issue2721Over  = 4 // images of 30 MiB, larger than a drawn page is answered with
	issue2721Under = 8 // images of 12 MiB, under it
	// issue2721Times is the most one draw may allocate, in multiples of the
	// files its page loads: before the fix a draw of this site allocated
	// about fourteen times them, and after it about three.
	issue2721Times = 6
)

// issue2721Archive is a release whose index.html loads every image in its
// first screen.
func issue2721Archive(t *testing.T) []byte {
	t.Helper()
	var page strings.Builder
	page.WriteString("<!doctype html><html><body><h1>Large images (#2721)</h1>")
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	add := func(name string, size int) {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		chunk := make([]byte, 1<<20)
		for range size >> 20 {
			_, _ = rand.Read(chunk)
			if _, err := w.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		fmt.Fprintf(&page, `<img src="/%s" width="120" height="90">`, name)
	}
	for i := range issue2721Over {
		add(fmt.Sprintf("over-%d.png", i), 30<<20)
	}
	for i := range issue2721Under {
		add(fmt.Sprintf("under-%d.png", i), 12<<20)
	}
	page.WriteString("</body></html>")
	w, err := z.Create("index.html")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(page.String()))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// issue2721Allocated is the bytes every replica has allocated since it
// started, summed. Unlike the heap in use, it does not depend on when each
// replica last collected its garbage.
func issue2721Allocated(t *testing.T) float64 {
	t.Helper()
	total := 0.0
	for _, target := range metricsURLs() {
		body := scrapeOne(t, target)
		if !hasSeries(body, "go_memstats_alloc_bytes_total") {
			t.Fatalf("%s reports no go_memstats_alloc_bytes_total", target)
		}
		total += scrapeScalar(body, "go_memstats_alloc_bytes_total")
	}
	return total
}

// issue2721Tile reads an environment's tile, reporting whether one is drawn.
func issue2721Tile(t *testing.T, s *site2559, env string) bool {
	t.Helper()
	status, got := rest2559(t, keyAdmin2559, http.MethodGet, s.path("/environments/"+env+"/tile"), nil)
	if status == http.StatusOK {
		if raw, _ := got["_raw"].(string); !strings.HasPrefix(raw, "\x89PNG") {
			t.Fatalf("the %s tile is not a PNG: %.40q", env, raw)
		}
		return true
	}
	return false
}

// TestIssue2721_DrawingASiteOfLargeImagesHoldsBoundedMemory is the criterion.
func TestIssue2721_DrawingASiteOfLargeImagesHoldsBoundedMemory(t *testing.T) {
	s := newSite2559(t, keyAdmin2559)
	id := upload2559(t, keyAdmin2559, s.name+".zip", issue2721Archive(t))
	status, job := rest2559(t, keyAdmin2559, http.MethodPost, s.path("/publish"), map[string]any{
		"environment": "dev", "note": "#2721", "archive": "mcp:resource:" + id, "source": s.name + ".zip",
	})
	want2559(t, "starting the publish", status, http.StatusAccepted, job)
	jobID, _ := job["id"].(string)
	if done := s.waitJob(t, keyAdmin2559, jobID); done["status"] != "done" {
		t.Fatalf("the publish ended %v", done["_raw"])
	}
	deadline := time.Now().Add(3 * time.Minute)
	for !issue2721Tile(t, s, "dev") {
		if time.Now().After(deadline) {
			t.Fatal("the dev tile was not drawn within 3 minutes")
		}
		time.Sleep(time.Second)
	}

	before := issue2721Allocated(t)
	s.promote(t, keyAdmin2559, "dev", "prod")
	deadline = time.Now().Add(3 * time.Minute)
	for !issue2721Tile(t, s, "prod") {
		if time.Now().After(deadline) {
			t.Fatal("the prod tile was not drawn within 3 minutes")
		}
		time.Sleep(200 * time.Millisecond)
	}
	site := float64((issue2721Over*30 + issue2721Under*12) << 20)
	allocated := issue2721Allocated(t) - before
	t.Logf("drawing the tile of a %.0f MiB site allocated %.0f MiB across the replicas", site/(1<<20), allocated/(1<<20))
	if allocated > issue2721Times*site {
		t.Errorf("drawing one tile allocated %.0f MiB, more than %d times the %.0f MiB of files its page loads",
			allocated/(1<<20), issue2721Times, site/(1<<20))
	}
}
