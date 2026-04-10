package portal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// inMemoryCollectionStore implements portal.CollectionStore for testing.
type inMemoryCollectionStore struct {
	collections map[string]portal.Collection
	sections    map[string][]portal.CollectionSection // keyed by collection ID
	insertErr   error
	updateErr   error
	deleteErr   error
	setSectErr  error
	listErr     error
}

func newInMemoryCollectionStore() *inMemoryCollectionStore {
	return &inMemoryCollectionStore{
		collections: make(map[string]portal.Collection),
		sections:    make(map[string][]portal.CollectionSection),
	}
}

func (s *inMemoryCollectionStore) Insert(_ context.Context, c portal.Collection) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.collections[c.ID] = c
	return nil
}

func (s *inMemoryCollectionStore) Get(_ context.Context, id string) (*portal.Collection, error) {
	c, ok := s.collections[id]
	if !ok {
		return nil, notFoundError{}
	}
	c.Sections = s.sections[id]
	return &c, nil
}

func (s *inMemoryCollectionStore) List(_ context.Context, filter portal.CollectionFilter) ([]portal.Collection, int, error) {
	if s.listErr != nil {
		return nil, 0, s.listErr
	}
	var result []portal.Collection
	for _, c := range s.collections {
		if c.DeletedAt != nil {
			continue
		}
		if filter.OwnerID != "" && c.OwnerID != filter.OwnerID {
			continue
		}
		result = append(result, c)
	}
	return result, len(result), nil
}

func (s *inMemoryCollectionStore) Update(_ context.Context, id, name, description string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	c, ok := s.collections[id]
	if !ok || c.DeletedAt != nil {
		return notFoundError{}
	}
	c.Name = name
	c.Description = description
	s.collections[id] = c
	return nil
}

func (s *inMemoryCollectionStore) UpdateConfig(_ context.Context, id string, config portal.CollectionConfig) error {
	c, ok := s.collections[id]
	if !ok {
		return notFoundError{}
	}
	c.Config = config
	s.collections[id] = c
	return nil
}

func (s *inMemoryCollectionStore) UpdateThumbnail(_ context.Context, id, thumbnailS3Key string) error {
	c, ok := s.collections[id]
	if !ok {
		return notFoundError{}
	}
	c.ThumbnailS3Key = thumbnailS3Key
	s.collections[id] = c
	return nil
}

func (s *inMemoryCollectionStore) SoftDelete(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	c, ok := s.collections[id]
	if !ok || c.DeletedAt != nil {
		return notFoundError{}
	}
	now := time.Now()
	c.DeletedAt = &now
	s.collections[id] = c
	return nil
}

func (s *inMemoryCollectionStore) SetSections(_ context.Context, collectionID string, sections []portal.CollectionSection) error {
	if s.setSectErr != nil {
		return s.setSectErr
	}
	s.sections[collectionID] = sections
	return nil
}

var _ portal.CollectionStore = (*inMemoryCollectionStore)(nil)

func collCtx(userID, email string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:    userID,
		UserEmail: email,
	})
}

func toolkitWithCollections(cs *inMemoryCollectionStore) *Toolkit {
	return New(Config{
		Name:            "test",
		CollectionStore: cs,
		S3Bucket:        "bucket",
		BaseURL:         "http://example.com",
	})
}

func extractJSON(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func extractError(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := result.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
	require.True(t, ok)
	return tc.Text
}

// --- create_collection tests ---

func TestCreateCollection_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:      "create_collection",
		Name:        "My Collection",
		Description: "A test collection",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	assert.NotEmpty(t, out["collection_id"])
	assert.Equal(t, "Collection created successfully.", out["message"])
	assert.Contains(t, out["portal_url"], out["collection_id"])

	// Verify stored
	collID := out["collection_id"].(string) //nolint:errcheck // test assertion
	coll, getErr := cs.Get(context.Background(), collID)
	require.NoError(t, getErr)
	assert.Equal(t, "user1", coll.OwnerID)
	assert.Equal(t, "user1@example.com", coll.OwnerEmail)
	assert.Equal(t, "My Collection", coll.Name)
}

func TestCreateCollection_WithSections(t *testing.T) {
	cs := newInMemoryCollectionStore()
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "create_collection",
		Name:   "With Sections",
		Sections: []sectionInput{
			{
				Title: "Section 1",
				Items: []itemInput{{AssetID: "asset-a"}, {AssetID: "asset-b"}},
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	collID := out["collection_id"].(string) //nolint:errcheck // test assertion

	sections := cs.sections[collID]
	require.Len(t, sections, 1)
	assert.Equal(t, "Section 1", sections[0].Title)
	require.Len(t, sections[0].Items, 2)
	assert.Equal(t, "asset-a", sections[0].Items[0].AssetID)
}

func TestCreateCollection_MissingName(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "create_collection",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "name is required")
}

func TestCreateCollection_InsertError(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.insertErr = notFoundError{}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "create_collection",
		Name:   "Failing",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "failed to create collection")
}

func TestCreateCollection_SetSectionsError(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.setSectErr = errors.New("db timeout")
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:   "create_collection",
		Name:     "Failing Sections",
		Sections: []sectionInput{{Title: "S1", Items: []itemInput{{AssetID: "a1"}}}},
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)

	errText := extractError(t, result)
	// Error must include collection_id so agent can retry set_sections
	assert.Contains(t, errText, "created but failed to set sections")
}

func TestCreateCollection_InvalidSections(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:   "create_collection",
		Name:     "Bad Sections",
		Sections: []sectionInput{{Title: "S1", Items: []itemInput{{AssetID: ""}}}},
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "asset_id is required")
}

func TestCreateCollection_NoBaseURL(t *testing.T) {
	cs := newInMemoryCollectionStore()
	tk := New(Config{
		Name:            "test",
		CollectionStore: cs,
		S3Bucket:        "bucket",
	})
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "create_collection",
		Name:   "No URL",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	_, hasURL := out["portal_url"]
	assert.False(t, hasURL)
}

// --- list_collections tests ---

func TestListCollections_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{ID: "c1", OwnerID: "user1", Name: "Coll 1"}
	cs.collections["c2"] = portal.Collection{ID: "c2", OwnerID: "user1", Name: "Coll 2"}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "list_collections",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	collections := out["collections"].([]any) //nolint:errcheck // test assertion
	assert.Len(t, collections, 2)
	assert.Equal(t, float64(2), out["total"])
}

func TestListCollections_Empty(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "list_collections",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	collections := out["collections"].([]any) //nolint:errcheck // test assertion
	assert.Len(t, collections, 0)             // empty array, not nil
}

func TestListCollections_FiltersByOwner(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{ID: "c1", OwnerID: "user1", Name: "Mine"}
	cs.collections["c2"] = portal.Collection{ID: "c2", OwnerID: "user2", Name: "Theirs"}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "list_collections",
	})
	require.NoError(t, err)

	out := extractJSON(t, result)
	assert.Equal(t, float64(1), out["total"])
}

func TestListCollections_StoreError(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.listErr = errors.New("db connection lost")
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "list_collections",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "failed to list collections")
}

// --- get_collection tests ---

func TestGetCollection_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Test Coll",
	}
	cs.sections["c1"] = []portal.CollectionSection{
		{ID: "s1", Title: "Section One", Items: []portal.CollectionItem{{ID: "i1", AssetID: "a1"}}},
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "get_collection", CollectionID: "c1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	assert.Equal(t, "Test Coll", out["name"])
	sections := out["sections"].([]any) //nolint:errcheck // test assertion
	assert.Len(t, sections, 1)
}

func TestGetCollection_MissingID(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "get_collection",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection_id is required")
}

func TestGetCollection_NotFound(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "get_collection", CollectionID: "nonexistent",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection not found")
}

func TestGetCollection_Deleted(t *testing.T) {
	cs := newInMemoryCollectionStore()
	now := time.Now()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Deleted", DeletedAt: &now,
	}
	tk := toolkitWithCollections(cs)

	result, _, err := tk.handleManageArtifact(context.Background(), nil, manageArtifactInput{
		Action: "get_collection", CollectionID: "c1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection has been deleted")
}

// --- update_collection tests ---

func TestUpdateCollection_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Old Name", Description: "Old Desc",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection", CollectionID: "c1", Name: "New Name",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	// Returns full collection object
	out := extractJSON(t, result)
	assert.Equal(t, "New Name", out["name"])
	assert.Equal(t, "Old Desc", out["description"])
}

func TestUpdateCollection_DescriptionOnly(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Keep This", Description: "Old Desc",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection", CollectionID: "c1", Description: "New Desc",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	assert.Equal(t, "Keep This", out["name"])
	assert.Equal(t, "New Desc", out["description"])
}

func TestUpdateCollection_MissingID(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection_id is required")
}

func TestUpdateCollection_WrongOwner(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "owner-xyz", Name: "Theirs",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("attacker", "attacker@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection", CollectionID: "c1", Name: "Stolen",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "you can only update your own collections")
}

func TestUpdateCollection_NotFound(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection", CollectionID: "nonexistent", Name: "X",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection not found")
}

func TestUpdateCollection_Deleted(t *testing.T) {
	cs := newInMemoryCollectionStore()
	now := time.Now()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Gone", DeletedAt: &now,
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection", CollectionID: "c1", Name: "Revive?",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection has been deleted")
}

func TestUpdateCollection_StoreError(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Current",
	}
	cs.updateErr = errors.New("db timeout")
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "update_collection", CollectionID: "c1", Name: "Attempt",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "failed to update collection")
}

// --- delete_collection tests ---

func TestDeleteCollection_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "To Delete",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete_collection", CollectionID: "c1",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	assert.Equal(t, "Collection deleted successfully.", out["message"])

	coll := cs.collections["c1"]
	assert.NotNil(t, coll.DeletedAt)
}

func TestDeleteCollection_MissingID(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete_collection",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection_id is required")
}

func TestDeleteCollection_WrongOwner(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "owner-xyz", Name: "Theirs",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("attacker", "attacker@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete_collection", CollectionID: "c1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "you can only delete your own collections")
}

func TestDeleteCollection_Deleted(t *testing.T) {
	cs := newInMemoryCollectionStore()
	now := time.Now()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Already Gone", DeletedAt: &now,
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete_collection", CollectionID: "c1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection has been deleted")
}

func TestDeleteCollection_StoreError(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Failing",
	}
	cs.deleteErr = notFoundError{}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "delete_collection", CollectionID: "c1",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "failed to delete collection")
}

// --- set_sections tests ---

func TestSetSections_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "My Coll",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:       "set_sections",
		CollectionID: "c1",
		Sections: []sectionInput{
			{Title: "Charts", Items: []itemInput{{AssetID: "a1"}}},
			{Title: "Reports", Description: "Monthly reports", Items: []itemInput{{AssetID: "a2"}, {AssetID: "a3"}}},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	out := extractJSON(t, result)
	assert.Equal(t, "My Coll", out["name"])

	sections := cs.sections["c1"]
	require.Len(t, sections, 2)
	assert.Equal(t, "Charts", sections[0].Title)
	assert.Len(t, sections[0].Items, 1)
	assert.Equal(t, "Reports", sections[1].Title)
	assert.Len(t, sections[1].Items, 2)
}

func TestSetSections_ClearSections(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "My Coll",
	}
	cs.sections["c1"] = []portal.CollectionSection{{ID: "old", Title: "Old"}}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:       "set_sections",
		CollectionID: "c1",
		Sections:     []sectionInput{},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.Len(t, cs.sections["c1"], 0)
}

func TestSetSections_MissingID(t *testing.T) {
	tk := toolkitWithCollections(newInMemoryCollectionStore())
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action: "set_sections",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection_id is required")
}

func TestSetSections_WrongOwner(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "owner-xyz", Name: "Theirs",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("attacker", "attacker@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:       "set_sections",
		CollectionID: "c1",
		Sections:     []sectionInput{{Title: "S1", Items: []itemInput{{AssetID: "a1"}}}},
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "you can only modify your own collections")
}

func TestSetSections_Deleted(t *testing.T) {
	cs := newInMemoryCollectionStore()
	now := time.Now()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "Gone", DeletedAt: &now,
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:       "set_sections",
		CollectionID: "c1",
		Sections:     []sectionInput{{Title: "S1", Items: []itemInput{{AssetID: "a1"}}}},
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "collection has been deleted")
}

func TestSetSections_InvalidSection(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "My Coll",
	}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:       "set_sections",
		CollectionID: "c1",
		Sections:     []sectionInput{{Title: "S1", Items: []itemInput{{AssetID: ""}}}},
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "asset_id is required")
}

func TestSetSections_StoreError(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{
		ID: "c1", OwnerID: "user1", Name: "My Coll",
	}
	cs.setSectErr = notFoundError{}
	tk := toolkitWithCollections(cs)
	ctx := collCtx("user1", "user1@example.com")

	result, _, err := tk.handleManageArtifact(ctx, nil, manageArtifactInput{
		Action:       "set_sections",
		CollectionID: "c1",
		Sections:     []sectionInput{{Title: "S1", Items: []itemInput{{AssetID: "a1"}}}},
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, extractError(t, result), "failed to set sections")
}

// --- convertSections tests ---

func TestConvertSections_Valid(t *testing.T) {
	inputs := []sectionInput{
		{Title: "S1", Description: "Desc", Items: []itemInput{{AssetID: "a1"}, {AssetID: "a2"}}},
		{Title: "S2", Items: []itemInput{{AssetID: "a3"}}},
	}

	sections, err := convertSections(inputs)
	require.NoError(t, err)
	require.Len(t, sections, 2)
	assert.NotEmpty(t, sections[0].ID)
	assert.Equal(t, "S1", sections[0].Title)
	assert.Equal(t, "Desc", sections[0].Description)
	require.Len(t, sections[0].Items, 2)
	assert.NotEmpty(t, sections[0].Items[0].ID)
	assert.Equal(t, "a1", sections[0].Items[0].AssetID)
}

func TestConvertSections_EmptyAssetID(t *testing.T) {
	_, err := convertSections([]sectionInput{
		{Title: "S1", Items: []itemInput{{AssetID: ""}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "asset_id is required")
}

func TestConvertSections_EmptyInput(t *testing.T) {
	sections, err := convertSections([]sectionInput{})
	require.NoError(t, err)
	assert.Len(t, sections, 0)
}

// --- getActiveCollection tests ---

func TestGetActiveCollection_NotFound(t *testing.T) {
	cs := newInMemoryCollectionStore()
	tk := toolkitWithCollections(cs)

	coll, errResult := tk.getActiveCollection(context.Background(), "missing")
	assert.Nil(t, coll)
	assert.NotNil(t, errResult)
	assert.True(t, errResult.IsError)
}

func TestGetActiveCollection_Deleted(t *testing.T) {
	cs := newInMemoryCollectionStore()
	now := time.Now()
	cs.collections["c1"] = portal.Collection{ID: "c1", DeletedAt: &now}
	tk := toolkitWithCollections(cs)

	coll, errResult := tk.getActiveCollection(context.Background(), "c1")
	assert.Nil(t, coll)
	assert.NotNil(t, errResult)
	assert.Contains(t, extractError(&testing.T{}, errResult), "collection has been deleted")
}

func TestGetActiveCollection_Success(t *testing.T) {
	cs := newInMemoryCollectionStore()
	cs.collections["c1"] = portal.Collection{ID: "c1", Name: "Active"}
	tk := toolkitWithCollections(cs)

	coll, errResult := tk.getActiveCollection(context.Background(), "c1")
	assert.NotNil(t, coll)
	assert.Nil(t, errResult)
	assert.Equal(t, "Active", coll.Name)
}
