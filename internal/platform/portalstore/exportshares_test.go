package portalstore

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// TestExportShareStore_HoldsAnExportsLinkToTheSharingPermission: an
// export's create_public_link writes through a store that refuses a persona
// withholding sharing (#2241).
func TestExportShareStore_HoldsAnExportsLinkToTheSharingPermission(t *testing.T) {
	var none *Handle
	if none.ExportShareStore() != nil {
		t.Error("a nil handle answered a store")
	}
	h := &Handle{shareStore: portal.NewNoopShareStore(), sharingAllowed: func(name string) bool { return name == "analyst" }}
	store := h.ExportShareStore()
	denied := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{PersonaName: "viewer"})
	if err := store.Insert(denied, &portal.Share{}); err == nil {
		t.Error("a persona without sharing created an export's link")
	}
	allowed := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{PersonaName: "analyst"})
	if err := store.Insert(allowed, &portal.Share{}); err != nil {
		t.Errorf("a persona with sharing was refused: %v", err)
	}
}
