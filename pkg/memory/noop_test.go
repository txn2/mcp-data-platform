package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoopStore_Insert(t *testing.T) {
	store := NewNoopStore()
	err := store.Insert(context.Background(), Record{
		ID:      "test-id",
		Content: "Some test content",
		Status:  StatusActive,
	})
	assert.NoError(t, err)
}

func TestNoopStore_Get(t *testing.T) {
	store := NewNoopStore()
	record, err := store.Get(context.Background(), "any-id")
	assert.Nil(t, record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "memory record not found")
}

func TestNoopStore_Update(t *testing.T) {
	store := NewNoopStore()
	err := store.Update(context.Background(), "id", RecordUpdate{Content: "updated"})
	assert.NoError(t, err)
}

func TestNoopStore_Delete(t *testing.T) {
	store := NewNoopStore()
	err := store.Delete(context.Background(), "id")
	assert.NoError(t, err)
}

func TestNoopStore_List(t *testing.T) {
	store := NewNoopStore()
	records, total, err := store.List(context.Background(), Filter{})
	assert.NoError(t, err)
	assert.Nil(t, records)
	assert.Equal(t, 0, total)
}

func TestNoopStore_VectorSearch(t *testing.T) {
	store := NewNoopStore()
	results, err := store.VectorSearch(context.Background(), VectorQuery{})
	assert.NoError(t, err)
	assert.Nil(t, results)
}

func TestNoopStore_EntityLookup(t *testing.T) {
	store := NewNoopStore()
	records, err := store.EntityLookup(context.Background(), "urn:li:dataset:foo", "analyst")
	assert.NoError(t, err)
	assert.Nil(t, records)
}

func TestNoopStore_MarkStale(t *testing.T) {
	store := NewNoopStore()
	err := store.MarkStale(context.Background(), []string{"id-1"}, "reason")
	assert.NoError(t, err)
}

func TestNoopStore_MarkVerified(t *testing.T) {
	store := NewNoopStore()
	err := store.MarkVerified(context.Background(), []string{"id-1"})
	assert.NoError(t, err)
}

func TestNoopStore_Supersede(t *testing.T) {
	store := NewNoopStore()
	err := store.Supersede(context.Background(), "old-id", "new-id")
	assert.NoError(t, err)
}
