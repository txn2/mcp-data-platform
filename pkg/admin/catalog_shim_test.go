package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// TestCatalogRoutesMountedThroughAdminHandler proves registerCatalogRoutes
// actually reaches the catalogapi seam with working dependencies. The seam's
// own tests build their Config directly, so nothing there would catch this
// package handing it a nil store, the wrong mutability, or a missing decoder
// — the shim is exactly the seam the seam's tests cannot see.
func TestCatalogRoutesMountedThroughAdminHandler(t *testing.T) {
	t.Parallel()
	store := apicatalog.NewMemoryStore()
	h := NewHandler(Deps{
		APICatalogStore:   store,
		ConfigStore:       &mockConfigStore{mode: "database"},
		DatabaseAvailable: true,
	}, nil)

	// A write route: proves Mutable and the injected strict decoder arrived.
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "mounted", "name": "mounted", "display_name": "Mounted",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create through the admin mux: %d %s", res.Code, res.Body.String())
	}

	// A read route: proves the store the parent injected is the one serving.
	res = doJSON(t, h, http.MethodGet, "/api/v1/admin/api-catalogs", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list through the admin mux: %d %s", res.Code, res.Body.String())
	}
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "mounted" {
		t.Fatalf("expected the catalog created through the mux, got %+v", list)
	}
	if _, err := store.GetCatalog(context.Background(), "mounted"); err != nil {
		t.Errorf("the parent-injected store should hold the write: %v", err)
	}
}

// TestCatalogRoutesReadOnlyInFileMode proves the parent forwards its
// file-config mode as Mutable=false, so the seam registers reads only.
func TestCatalogRoutesReadOnlyInFileMode(t *testing.T) {
	t.Parallel()
	h := NewHandler(Deps{
		APICatalogStore:   apicatalog.NewMemoryStore(),
		ConfigStore:       &mockConfigStore{mode: "file"},
		DatabaseAvailable: true,
	}, nil)
	res := doJSON(t, h, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id": "x", "name": "x", "display_name": "X",
	})
	if res.Code == http.StatusCreated {
		t.Fatal("file config mode must not expose the catalog write routes")
	}
}
