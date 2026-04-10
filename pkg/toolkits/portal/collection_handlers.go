package portal

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// collectionDeletedMsg is returned when an operation targets a soft-deleted collection.
const collectionDeletedMsg = "collection has been deleted"

// validationMsgFmt formats a validation error for MCP error responses.
const validationMsgFmt = "validation: %s"

// getActiveCollection fetches a collection and rejects soft-deleted ones.
func (t *Toolkit) getActiveCollection(ctx context.Context, id string) (*portal.Collection, *mcp.CallToolResult) {
	coll, err := t.collectionStore.Get(ctx, id)
	if err != nil {
		return nil, errorResult("collection not found: " + err.Error())
	}
	if coll.DeletedAt != nil {
		return nil, errorResult(collectionDeletedMsg)
	}
	return coll, nil
}

func (t *Toolkit) handleCreateCollection(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if err := portal.ValidateCollectionName(input.Name); err != nil {
		return errorResult(fmt.Sprintf(validationMsgFmt, err)), nil, nil
	}
	if err := portal.ValidateCollectionDescription(input.Description); err != nil {
		return errorResult(fmt.Sprintf(validationMsgFmt, err)), nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	ownerEmail := resolveOwnerEmail(ctx)

	collID, err := generateID()
	if err != nil {
		return errorResult("internal error generating collection ID"), nil, nil //nolint:nilerr // MCP protocol
	}

	coll := portal.Collection{
		ID:          collID,
		OwnerID:     ownerID,
		OwnerEmail:  ownerEmail,
		Name:        input.Name,
		Description: input.Description,
	}

	if err := t.collectionStore.Insert(ctx, coll); err != nil {
		return errorResult("failed to create collection: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if len(input.Sections) > 0 {
		sections, err := convertSections(input.Sections)
		if err != nil {
			return errorResult(fmt.Sprintf(validationMsgFmt, err)), nil, nil //nolint:nilerr // MCP protocol
		}
		if err := t.collectionStore.SetSections(ctx, collID, sections); err != nil {
			// Include collection_id so the agent can retry set_sections on the orphaned collection.
			return errorResult(fmt.Sprintf(
				"collection %s created but failed to set sections: %s", collID, err.Error(),
			)), nil, nil //nolint:nilerr // MCP protocol
		}
	}

	result := map[string]any{
		"collection_id": collID,
		"message":       "Collection created successfully.",
	}
	if t.baseURL != "" {
		result["portal_url"] = t.baseURL + "/portal/collections/" + collID
	}
	return jsonResult(result)
}

func (t *Toolkit) handleListCollections(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	ownerID := resolveOwnerID(ctx)

	collections, total, err := t.collectionStore.List(ctx, portal.CollectionFilter{
		OwnerID: ownerID,
		Search:  input.Search,
		Limit:   input.Limit,
		Offset:  input.Offset,
	})
	if err != nil {
		return errorResult("failed to list collections: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if collections == nil {
		collections = []portal.Collection{}
	}

	return jsonResult(map[string]any{
		"collections": collections,
		"total":       total,
	})
}

// handleGetCollection retrieves a collection by ID. No ownership check — read
// access is intentionally broader than write, matching the asset get behavior.
func (t *Toolkit) handleGetCollection(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.CollectionID == "" {
		return errorResult("collection_id is required for get_collection action"), nil, nil
	}

	coll, errResult := t.getActiveCollection(ctx, input.CollectionID)
	if errResult != nil {
		return errResult, nil, nil
	}

	return jsonResult(coll)
}

func (t *Toolkit) handleUpdateCollection(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.CollectionID == "" {
		return errorResult("collection_id is required for update_collection action"), nil, nil
	}

	coll, errResult := t.getActiveCollection(ctx, input.CollectionID)
	if errResult != nil {
		return errResult, nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if coll.OwnerID != ownerID {
		return errorResult("you can only update your own collections"), nil, nil
	}

	name := coll.Name
	if input.Name != "" {
		name = input.Name
	}
	desc := coll.Description
	if input.Description != "" {
		desc = input.Description
	}

	if err := portal.ValidateCollectionName(name); err != nil {
		return errorResult(fmt.Sprintf(validationMsgFmt, err)), nil, nil
	}
	if err := portal.ValidateCollectionDescription(desc); err != nil {
		return errorResult(fmt.Sprintf(validationMsgFmt, err)), nil, nil
	}

	if err := t.collectionStore.Update(ctx, input.CollectionID, name, desc); err != nil {
		return errorResult("failed to update collection: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	updated, err := t.collectionStore.Get(ctx, input.CollectionID)
	if err != nil {
		return errorResult("updated but failed to retrieve collection: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(updated)
}

func (t *Toolkit) handleDeleteCollection(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.CollectionID == "" {
		return errorResult("collection_id is required for delete_collection action"), nil, nil
	}

	coll, errResult := t.getActiveCollection(ctx, input.CollectionID)
	if errResult != nil {
		return errResult, nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if coll.OwnerID != ownerID {
		return errorResult("you can only delete your own collections"), nil, nil
	}

	if err := t.collectionStore.SoftDelete(ctx, input.CollectionID); err != nil {
		return errorResult("failed to delete collection: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"collection_id": input.CollectionID,
		"message":       "Collection deleted successfully.",
	})
}

func (t *Toolkit) handleSetSections(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.CollectionID == "" {
		return errorResult("collection_id is required for set_sections action"), nil, nil
	}

	coll, errResult := t.getActiveCollection(ctx, input.CollectionID)
	if errResult != nil {
		return errResult, nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if coll.OwnerID != ownerID {
		return errorResult("you can only modify your own collections"), nil, nil
	}

	sections, err := convertSections(input.Sections)
	if err != nil {
		return errorResult(fmt.Sprintf(validationMsgFmt, err)), nil, nil //nolint:nilerr // MCP protocol
	}

	if err := t.collectionStore.SetSections(ctx, input.CollectionID, sections); err != nil {
		return errorResult("failed to set sections: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	updated, err := t.collectionStore.Get(ctx, input.CollectionID)
	if err != nil {
		return errorResult("sections updated but failed to retrieve collection: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(updated)
}

// convertSections transforms MCP input sections into portal CollectionSection
// values with generated IDs, validating each section and item.
func convertSections(inputs []sectionInput) ([]portal.CollectionSection, error) {
	sections := make([]portal.CollectionSection, len(inputs))
	for i, s := range inputs {
		items := make([]portal.CollectionItem, len(s.Items))
		for j, item := range s.Items {
			if item.AssetID == "" {
				return nil, fmt.Errorf("section %d, item %d: asset_id is required", i, j)
			}
			itemID, err := generateID()
			if err != nil {
				return nil, fmt.Errorf("generating item ID: %w", err)
			}
			items[j] = portal.CollectionItem{ID: itemID, AssetID: item.AssetID}
		}
		secID, err := generateID()
		if err != nil {
			return nil, fmt.Errorf("generating section ID: %w", err)
		}
		sections[i] = portal.CollectionSection{
			ID:          secID,
			Title:       s.Title,
			Description: s.Description,
			Items:       items,
		}
	}
	if err := portal.ValidateSections(sections); err != nil {
		return nil, fmt.Errorf("validating sections: %w", err)
	}
	return sections, nil
}
