package resourceholds_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceholds"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// fakeRefs answers the asset-reference reverse lookup, honoring the bound the
// caller passes so a test can exercise the cut answer the real store gives.
type fakeRefs struct {
	rows      int
	kind      assetrefs.TargetKind
	target    string
	gotLimit  int
	returnErr error
}

func (f *fakeRefs) ListByTarget(
	_ context.Context, kind assetrefs.TargetKind, targetID string, limit int,
) ([]assetrefs.Ref, error) {
	f.kind, f.target, f.gotLimit = kind, targetID, limit
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	n := min(f.rows, limit)
	out := make([]assetrefs.Ref, 0, n)
	for i := range n {
		out = append(out, assetrefs.Ref{AssetID: "asset-" + strconv.Itoa(i)})
	}
	return out, nil
}

type fakeAttachments struct {
	rows      int
	got       string
	returnErr error
}

func (f *fakeAttachments) ListByResource(_ context.Context, resourceID string) ([]string, error) {
	f.got = resourceID
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	out := make([]string, 0, f.rows)
	for i := range f.rows {
		out = append(out, "prompt-"+strconv.Itoa(i))
	}
	return out, nil
}

func TestResourceHoldsCountsEachKind(t *testing.T) {
	refs := &fakeRefs{rows: 2}
	attach := &fakeAttachments{rows: 3}
	c := resourceholds.New(resourceholds.Deps{Refs: refs, Attachments: attach})

	holds, err := c.ResourceHolds(t.Context(), "res-1")

	require.NoError(t, err)
	assert.Equal(t, 2, holds.Assets)
	assert.Equal(t, 3, holds.Prompts)
	assert.False(t, holds.More)
	assert.Equal(t, assetrefs.TargetResource, refs.kind, "an asset reference to a resource, not to an asset")
	assert.Equal(t, "res-1", refs.target)
	assert.Equal(t, "res-1", attach.got)
}

func TestResourceHoldsSaysWhenACountWasCut(t *testing.T) {
	c := resourceholds.New(resourceholds.Deps{Refs: &fakeRefs{rows: 500}})

	holds, err := c.ResourceHolds(t.Context(), "res-1")

	require.NoError(t, err)
	assert.True(t, holds.More, "a short count read as a complete one is what this flag prevents")
	assert.Positive(t, holds.Assets)
}

func TestResourceHoldsWithNoLayersAtAll(t *testing.T) {
	c := resourceholds.New(resourceholds.Deps{})

	holds, err := c.ResourceHolds(t.Context(), "res-1")

	require.NoError(t, err)
	assert.Zero(t, holds.Assets+holds.Prompts,
		"a deployment with neither of these layers has nothing pointing at anything")
}

func TestResourceHoldsReportsAFailedLookup(t *testing.T) {
	boom := errors.New("connection reset")
	for name, deps := range map[string]resourceholds.Deps{
		"assets":  {Refs: &fakeRefs{returnErr: boom}},
		"prompts": {Attachments: &fakeAttachments{returnErr: boom}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resourceholds.New(deps).ResourceHolds(t.Context(), "res-1")

			require.Error(t, err, "an empty count would read as nothing depends on this file")
			assert.ErrorIs(t, err, boom)
		})
	}
}
