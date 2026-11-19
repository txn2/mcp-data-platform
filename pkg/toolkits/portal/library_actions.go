package portal

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/library"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The manage_asset actions over a person's library (#2504, #2509).
const (
	actionListRecents   = "list_recents"
	actionListFavorites = "list_favorites"
	actionListTrash     = "list_trash"
	actionRestore       = "restore"
)

// Library is a person's library as manage_asset reads it: recents and
// favorites under the reach rule, and the trash an owner restores from
// (internal/portal/library implements it).
type Library interface {
	Recents(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]library.Item, error)
	Favorites(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]library.Item, error)
	Trash(ctx context.Context, scope library.Scope, retention time.Duration, limit, offset int) (library.Page[library.TrashItem], error)
	Restore(ctx context.Context, kind library.Kind, id string) error
}

// libraryAccess is the library and the trash retention the toolkit reports.
type libraryAccess struct {
	store     Library
	retention time.Duration
}

// SetLibrary gives manage_asset its library actions. Without it they report
// that the deployment keeps no library.
func (t *Toolkit) SetLibrary(l Library, retention time.Duration) {
	if l == nil {
		return
	}
	t.library = &libraryAccess{store: l, retention: retention}
	t.actions[actionListRecents] = t.handleListRecents
	t.actions[actionListFavorites] = t.handleListFavorites
	t.actions[actionListTrash] = t.handleListTrash
	t.actions[actionRestore] = t.handleRestore
}

// libraryCaller is the person a library action is for, or the refusal when
// the call carries nobody (a managed-script run reads no person's library).
func libraryCaller(ctx context.Context) (userKey string, reach *portaldomain.Reach, denial *mcp.CallToolResult) {
	pc := middleware.GetPlatformContext(ctx)
	reach = callerReach(ctx, string(portaldomain.ScopeAll), portaldomain.ScopeAll)
	if pc == nil || reach == nil {
		return "", nil, middleware.UnauthorizedResult(
			"a user identity is required to read a library",
			"Authenticate as a person: recents, favorites and the trash are a person's own.")
	}
	return library.UserKey(pc.UserID, pc.UserEmail), reach, nil
}

func (t *Toolkit) handleListRecents(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	return t.listItems(ctx, input, t.library.store.Recents)
}

func (t *Toolkit) handleListFavorites(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	return t.listItems(ctx, input, t.library.store.Favorites)
}

type libraryItems func(ctx context.Context, userID string, reach *portaldomain.Reach, limit int) ([]library.Item, error)

func (*Toolkit) listItems(ctx context.Context, input manageAssetInput, read libraryItems) (*mcp.CallToolResult, any, error) {
	key, reach, denial := libraryCaller(ctx)
	if denial != nil {
		return denial, nil, nil
	}
	items, err := read(ctx, key, reach, input.Limit)
	if err != nil {
		return toolkit.ErrorResult("failed to read the library: " + err.Error()), nil, nil
	}
	total := len(items)
	return toolkit.JSONResultTyped(map[string]any{"items": items, "total": total})
}

func (t *Toolkit) handleListTrash(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if _, _, denial := libraryCaller(ctx); denial != nil {
		return denial, nil, nil
	}
	owner, _ := callerAssetScope(ctx)
	page, err := t.library.store.Trash(ctx, library.Scope{Owner: owner}, t.library.retention, input.Limit, input.Offset)
	if err != nil {
		return toolkit.ErrorResult("failed to read the trash: " + err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(map[string]any{
		"items": page.Items, "total": page.Total, "retention_days": int(t.library.retention / (24 * time.Hour)),
	})
}

// handleRestore takes an asset (asset_id) or a collection (collection_id) out
// of the caller's trash. Its owner or an administrator only; an item that is
// not in the trash, or not the caller's, reads as not found.
func (t *Toolkit) handleRestore(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	item := t.deletedItem(ctx, input)
	kind, id := item.kind, item.id
	if id == "" {
		return toolkit.ErrorResult("asset_id or collection_id is required for restore action"), nil, nil
	}
	if !item.found || (!t.isAdmin(ctx) && !ownsResource(ctx, item.ownerID, item.ownerEmail)) {
		return middleware.NotFoundResult("not in your trash: "+id,
			"list_trash lists what you can restore."), nil, nil
	}
	err := t.library.store.Restore(ctx, kind, id)
	if errors.Is(err, library.ErrNotDeleted) {
		return middleware.NotFoundResult("not in your trash: "+id, "list_trash lists what you can restore."), nil, nil
	}
	if err != nil {
		return toolkit.ErrorResult("failed to restore: " + err.Error()), nil, nil
	}
	return toolkit.JSONResultTyped(map[string]any{
		"kind": kind, "id": id, fieldMessage: "Restored with its versions, shares and collection memberships.",
	})
}

// deletedRef is the item a restore names, and whether it exists and is
// deleted.
type deletedRef struct {
	kind                library.Kind
	id                  string
	ownerID, ownerEmail string
	found               bool
}

// deletedItem reads the item a restore names, reporting found only when it
// exists and is deleted.
func (t *Toolkit) deletedItem(ctx context.Context, input manageAssetInput) deletedRef {
	if input.CollectionID != "" && input.AssetID == "" {
		c, err := t.collectionStore.Get(ctx, input.CollectionID)
		if err != nil || c == nil || c.DeletedAt == nil {
			return deletedRef{kind: library.KindCollection, id: input.CollectionID}
		}
		return deletedRef{kind: library.KindCollection, id: c.ID, ownerID: c.OwnerID, ownerEmail: c.OwnerEmail, found: true}
	}
	a, err := t.assetStore.Get(ctx, input.AssetID)
	if input.AssetID == "" || err != nil || a == nil || a.DeletedAt == nil {
		return deletedRef{kind: library.KindAsset, id: input.AssetID}
	}
	return deletedRef{kind: library.KindAsset, id: a.ID, ownerID: a.OwnerID, ownerEmail: a.OwnerEmail, found: true}
}
