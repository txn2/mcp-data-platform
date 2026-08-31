package producedby

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder captures what Note handed the store.
type recorder struct {
	writes []Write
	err    error
}

func (r *recorder) Record(_ context.Context, w Write) error {
	r.writes = append(r.writes, w)
	return r.err
}

func (*recorder) ListByTarget(context.Context, string, string) ([]Row, error) { return nil, nil }

func (*recorder) ListByProducer(context.Context, string, string, int) ([]Row, error) {
	return nil, nil
}

func TestNoteRecordsTheContextProducer(t *testing.T) {
	rec := &recorder{}
	ctx := With(context.Background(), Producer{Kind: KindScript, ID: "script-1", Label: "daily"})
	Note(ctx, rec, Write{TargetKind: TargetAsset, TargetID: "asset-1", Created: true, Version: 1})

	require.Len(t, rec.writes, 1)
	assert.Equal(t, "script-1", rec.writes[0].Producer.ID)
	assert.Equal(t, "asset-1", rec.writes[0].TargetID)
	assert.True(t, rec.writes[0].Created)
}

func TestNoteWithoutProducerRecordsNothing(t *testing.T) {
	rec := &recorder{}
	Note(context.Background(), rec, Write{TargetKind: TargetAsset, TargetID: "asset-1"})
	assert.Empty(t, rec.writes, "a call that names no producer records nothing")
}

func TestNoteWithoutStoreIsInert(t *testing.T) {
	ctx := With(context.Background(), Producer{Kind: KindPerson, ID: "sub-1"})
	assert.NotPanics(t, func() {
		Note(ctx, nil, Write{TargetKind: TargetResource, TargetID: "res-1"})
	})
}

// TestNoteSwallowsStoreFailure is the whole contract of the recording path: the
// write it accompanies has already happened, so a failure here is logged and
// never returned.
func TestNoteSwallowsStoreFailure(t *testing.T) {
	rec := &recorder{err: errors.New("boom")}
	ctx := With(context.Background(), Producer{Kind: KindSession, ID: "sess-1"})
	assert.NotPanics(t, func() {
		Note(ctx, rec, Write{TargetKind: TargetAsset, TargetID: "asset-1"})
	})
	assert.Len(t, rec.writes, 1)
}
