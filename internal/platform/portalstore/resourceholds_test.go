package portalstore

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceholds"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// stubRefs answers the asset-reference reverse lookup, which is the half of the
// hold check this Handle owns a store for.
type stubRefs struct {
	assetrefs.Store
	rows      int
	returnErr error
}

func (s *stubRefs) ListByTarget(
	_ context.Context, _ assetrefs.TargetKind, _ string, _ int,
) ([]assetrefs.Ref, error) {
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return make([]assetrefs.Ref, s.rows), nil
}

// stubAttachments answers the prompt-attachment reverse lookup, the half that
// arrives from the prompt layer.
type stubAttachments struct{ rows int }

func (s *stubAttachments) ListByResource(context.Context, string) ([]string, error) {
	return make([]string, s.rows), nil
}

// TestHoldReaderCarriesTheCounts proves the adapter the bind installs carries
// every count through to the shape the asset toolkit asks for. The two types
// are the same four numbers, and a conversion that dropped one would leave a
// delete refusing on a zero however many assets referenced the file.
func TestHoldReaderCarriesTheCounts(t *testing.T) {
	reader := holdReader{checker: resourceholds.New(resourceholds.Deps{
		Refs: &stubRefs{rows: 2}, Attachments: &stubAttachments{rows: 1},
	})}

	holds, err := reader.ResourceHolds(context.Background(), "res-1")

	require.NoError(t, err)
	assert.Equal(t, 2, holds.Assets)
	assert.Equal(t, 1, holds.Prompts)
	assert.True(t, holds.Any())
}

func TestHoldReaderCarriesAFailedLookup(t *testing.T) {
	boom := errors.New("connection reset")
	reader := holdReader{checker: resourceholds.New(resourceholds.Deps{
		Refs: &stubRefs{returnErr: boom},
	})}

	_, err := reader.ResourceHolds(context.Background(), "res-1")

	require.Error(t, err, "an empty count would read as nothing depends on this file")
	assert.ErrorIs(t, err, boom)
}

// TestBindResourceHoldsIsSafeWithoutALayer covers the two shapes a deployment
// can be in when the composition root reaches this: no handle at all, and a
// handle whose toolkit was never built.
func TestBindResourceHoldsIsSafeWithoutALayer(t *testing.T) {
	var missing *Handle
	assert.NotPanics(t, func() { missing.BindResourceHolds(nil) })

	h := NewFromStores(Stores{ContentRefs: &stubRefs{}}, nil, Config{Name: "portal"})
	assert.NotPanics(t, func() { h.BindResourceHolds(&stubAttachments{}) })
}
