//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Issue #1796: one registration's page headed the file a table reads "What it
// reads" and omitted the source record's description, which pkg/resource and
// internal/portal/portaldomain already store and the `source` object the two
// table routes return did not carry.
//
// What these hold, against the running platform: GET /api/v1/tables and
// GET /api/v1/tables/{regID} both return the source record's description on
// the `source` object, filled from the resource's own description; a source
// with no description omits the field rather than carrying an empty one; and
// the two routes agree, since they are one projection.
//
// The listing's layout defects (a long qualified name painting over the cells
// beside it, and Registered and State as two columns) are browser-observable
// and are held by ui/e2e/interactive/scratch-tables-density.spec.ts, which
// measures the cell geometry at three widths. There is nothing on the wire to
// assert about them.
//
// Wire forms: every parameter these send admits exactly one form, so each is
// sent once. The two routes under test are REST GETs that take only a path
// parameter. manage_resource's `action`, `display_name`, `filename`, `path`,
// `content_type`, `content` and `description`, save_asset's `name`,
// `content_type` and `content`, and manage_table's `action`, `reference`,
// `connection`, `table_name` and `registration_id`, are all typed string.

const issue1796Purpose = "Acceptance for #1796: a registered table's source carries its own description."

// issue1796Description is what the resource says it is, and what the table
// routes must carry through.
const issue1796Description = "Acceptance fixture for #1796: three rows of store codes and their regions."

// issue1796CSV is a file a table can be registered over.
const issue1796CSV = "store_code,region,city\nS-001,West,Portland\nS-002,West,Seattle\nS-003,East,Boston\n"

// registerIssue1796Table uploads a file and registers a table over it,
// returning the registration id. description is what the record says it is,
// and is what the table routes must carry through; an empty one leaves the
// record without a description, which is the case the omission test needs.
func registerIssue1796Table(t *testing.T, c *client, description string) (regID string) {
	t.Helper()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	args := map[string]any{
		"action":       "create",
		"display_name": "Acceptance 1796 " + stamp,
		"filename":     "acc-1796-" + stamp + ".csv",
		"path":         "acceptance/issue-1796",
		"content_type": "text/csv",
		"content":      issue1796CSV,
	}
	if description != "" {
		args["description"] = description
	}
	created := c.call("manage_resource", args)
	reference, _ := created["reference"].(string)
	if reference == "" {
		t.Fatalf("manage_resource create returned no reference: %v", created)
	}

	reg := c.call("manage_table", map[string]any{
		"action":     "register",
		"reference":  reference,
		"connection": scratchResourceConnection,
		"table_name": "acc_1796_" + stamp,
	})
	regID, _ = reg["registration_id"].(string)
	if regID == "" {
		t.Fatalf("the registration returned no id: %v", reg)
	}
	t.Cleanup(func() {
		c.call("manage_table", map[string]any{"action": "unregister", "registration_id": regID})
	})
	return regID
}

// sourceOf reads the `source` object off a table row or a single registration.
func sourceOf(t *testing.T, row map[string]any) map[string]any {
	t.Helper()
	src, ok := row["source"].(map[string]any)
	if !ok {
		t.Fatalf("the registration carries no source object: %v", row)
	}
	return src
}

func TestIssue1796_OneRegistrationCarriesItsSourcesDescription(t *testing.T) {
	c := connect(t)
	regID := registerIssue1796Table(t, c, issue1796Description)

	status, out := c.rest(http.MethodGet, "/api/v1/tables/"+regID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /tables/%s: status %d: %v", regID, status, out)
	}
	src := sourceOf(t, out)
	if got, _ := src["description"].(string); got != issue1796Description {
		t.Fatalf("the source's description did not reach the route:\n got %q\nwant %q",
			got, issue1796Description)
	}
	// The name is still there beside it; the description is an addition, not a
	// replacement.
	if name, _ := src["name"].(string); name == "" {
		t.Fatalf("the source carries a description but no name: %v", src)
	}
}

func TestIssue1796_TheListingCarriesItToo(t *testing.T) {
	c := connect(t)
	regID := registerIssue1796Table(t, c, issue1796Description)

	status, out := c.rest(http.MethodGet, "/api/v1/tables", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /tables: status %d: %v", status, out)
	}
	rows, _ := out["data"].([]any)
	if len(rows) == 0 {
		t.Fatalf("the listing is empty: %v", out)
	}

	found := false
	for _, row := range rows {
		r, _ := row.(map[string]any)
		if id, _ := r["id"].(string); id != regID {
			continue
		}
		found = true
		src := sourceOf(t, r)
		if got, _ := src["description"].(string); got != issue1796Description {
			t.Fatalf("the listing's source carries %q, not the record's description", got)
		}
	}
	if !found {
		t.Fatalf("the registration %s is not in the listing", regID)
	}
}

func TestIssue1796_ASourceWithNoDescriptionOmitsTheField(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())

	// An ASSET, not a resource: manage_resource requires a description
	// ("validation: description is required"), so a resource with none cannot
	// be authored through the tool, while save_asset's is optional. It is also
	// the other source kind the listing spans, so this covers the asset half
	// of the same projection.
	saved := c.call("save_asset", map[string]any{
		"name":         "acc-1796-" + stamp,
		"content_type": "text/csv",
		"content":      issue1796CSV,
	})
	assetID, _ := saved["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("save_asset returned no asset: %v", saved)
	}
	reference, _ := saved["reference"].(string)
	if reference == "" {
		reference = "mcp:asset:" + assetID
	}

	// The asset-side scratch connection: a Hive catalog reads its metastore and
	// its tables through one S3 client, so the resources catalog (on the other
	// object store) cannot read a file the portal wrote.
	reg := c.call("manage_table", map[string]any{
		"action":     "register",
		"reference":  reference,
		"connection": "acme-scratch",
		"table_name": "acc_1796_asset_" + stamp,
	})
	regID, _ := reg["registration_id"].(string)
	if regID == "" {
		t.Fatalf("the registration returned no id: %v", reg)
	}
	t.Cleanup(func() {
		c.call("manage_table", map[string]any{"action": "unregister", "registration_id": regID})
	})

	status, out := c.rest(http.MethodGet, "/api/v1/tables/"+regID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /tables/%s: status %d: %v", regID, status, out)
	}
	src := sourceOf(t, out)
	// Omitted, not empty: a surface then has nothing to render rather than an
	// empty field to leave blank.
	if _, present := src["description"]; present {
		t.Fatalf("a source with no description carried the field: %v", src)
	}
}
