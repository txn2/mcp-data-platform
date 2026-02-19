package knowledge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDatasetURN = "urn:li:dataset:foo" //nolint:revive // test constant

// --- NoopDataHubWriter tests ---

func TestNoopDataHubWriter_GetCurrentMetadata(t *testing.T) {
	writer := &NoopDataHubWriter{}
	meta, err := writer.GetCurrentMetadata(context.Background(), testDatasetURN)
	require.NoError(t, err)
	require.NotNil(t, meta)

	// Verify all slices are initialized (not nil)
	assert.NotNil(t, meta.Tags)
	assert.NotNil(t, meta.GlossaryTerms)
	assert.NotNil(t, meta.Owners)

	// Verify they are empty
	assert.Empty(t, meta.Tags)
	assert.Empty(t, meta.GlossaryTerms)
	assert.Empty(t, meta.Owners)

	// Verify description is empty string
	assert.Equal(t, "", meta.Description)
}

func TestNoopDataHubWriter_UpdateDescription(t *testing.T) {
	writer := &NoopDataHubWriter{}
	err := writer.UpdateDescription(context.Background(), testDatasetURN, "new description")
	assert.NoError(t, err)
}

func TestNoopDataHubWriter_AddTag(t *testing.T) {
	writer := &NoopDataHubWriter{}
	err := writer.AddTag(context.Background(), testDatasetURN, "important")
	assert.NoError(t, err)
}

func TestNoopDataHubWriter_RemoveTag(t *testing.T) {
	writer := &NoopDataHubWriter{}
	err := writer.RemoveTag(context.Background(), testDatasetURN, "deprecated")
	assert.NoError(t, err)
}

func TestNoopDataHubWriter_AddGlossaryTerm(t *testing.T) {
	writer := &NoopDataHubWriter{}
	err := writer.AddGlossaryTerm(context.Background(), testDatasetURN, "urn:li:glossaryTerm:revenue")
	assert.NoError(t, err)
}

func TestNoopDataHubWriter_AddDocumentationLink(t *testing.T) {
	writer := &NoopDataHubWriter{}
	err := writer.AddDocumentationLink(
		context.Background(),
		testDatasetURN,
		"https://docs.example.com/foo",
		"Dataset documentation",
	)
	assert.NoError(t, err)
}

func TestNoopDataHubWriter_UpdateColumnDescription(t *testing.T) {
	writer := &NoopDataHubWriter{}
	err := writer.UpdateColumnDescription(context.Background(), testDatasetURN, "email", "Email address")
	assert.NoError(t, err)
}

// --- Interface compliance ---

func TestNoopDataHubWriter_ImplementsInterface(_ *testing.T) {
	var _ DataHubWriter = (*NoopDataHubWriter)(nil)
}

// --- GetCurrentMetadata returns correctly typed EntityMetadata ---

func TestNoopDataHubWriter_GetCurrentMetadata_SliceTypes(t *testing.T) {
	writer := &NoopDataHubWriter{}
	meta, err := writer.GetCurrentMetadata(context.Background(), "any-urn")
	require.NoError(t, err)

	// Verify the slices can be appended to (they are real slices, not nil)
	meta.Tags = append(meta.Tags, "test-tag")
	assert.Len(t, meta.Tags, 1)

	meta.GlossaryTerms = append(meta.GlossaryTerms, "urn:li:glossaryTerm:test")
	assert.Len(t, meta.GlossaryTerms, 1)

	meta.Owners = append(meta.Owners, "test-owner")
	assert.Len(t, meta.Owners, 1)
}

// --- Multiple calls return independent instances ---

func TestNoopDataHubWriter_GetCurrentMetadata_IndependentInstances(t *testing.T) {
	writer := &NoopDataHubWriter{}

	meta1, err1 := writer.GetCurrentMetadata(context.Background(), "urn:1")
	require.NoError(t, err1)

	meta2, err2 := writer.GetCurrentMetadata(context.Background(), "urn:2")
	require.NoError(t, err2)

	// Modifying one should not affect the other
	meta1.Tags = append(meta1.Tags, "modified")
	assert.Empty(t, meta2.Tags)
	assert.Len(t, meta1.Tags, 1)
}
