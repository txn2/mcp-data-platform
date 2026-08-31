package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// recordingProducers captures what the resource write funnels noted.
type recordingProducers struct{ writes []producedby.Write }

func (r *recordingProducers) Record(_ context.Context, w producedby.Write) error {
	r.writes = append(r.writes, w)
	return nil
}

func (*recordingProducers) ListByTarget(context.Context, string, string) ([]producedby.Row, error) {
	return nil, nil
}

func (*recordingProducers) ListByProducer(context.Context, string, string, int) ([]producedby.Row, error) {
	return nil, nil
}

// TestProducerContextNamesThePersonAtTheKeyboard is acceptance criterion 4 at
// the seam that decides it: an upload through the resources API is recorded
// against the person who made it.
func TestProducerContextNamesThePersonAtTheKeyboard(t *testing.T) {
	claims := &Claims{Sub: "sub-1", Email: "a@example.com"}
	got, ok := producedby.From(producerContext(context.Background(), claims))
	require.True(t, ok)
	assert.Equal(t, producedby.KindPerson, got.Kind)
	assert.Equal(t, "sub-1", got.ID)
	assert.Equal(t, "a@example.com", got.Label)
}

// TestProducerContextKeepsTheStampedProducer pins the precedence: the surface
// that stamped the context knew the script id, and these claims carry only the
// principal's name.
func TestProducerContextKeepsTheStampedProducer(t *testing.T) {
	ctx := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-1", Label: "daily-sales",
	})
	claims := &Claims{Sub: "script:daily-sales", Email: "owner@example.com", OnBehalfOf: "owner@example.com"}
	got, ok := producedby.From(producerContext(ctx, claims))
	require.True(t, ok)
	assert.Equal(t, "script-1", got.ID)
}

// TestProducerContextRefusesToGuessForAnUnattendedCaller keeps a run's write
// out of the record when the surface named no producer: filing it under the
// author would report a person who was not there.
func TestProducerContextRefusesToGuessForAnUnattendedCaller(t *testing.T) {
	claims := &Claims{Sub: "script:daily-sales", Email: "owner@example.com", OnBehalfOf: "owner@example.com"}
	assert.False(t, producedby.Has(producerContext(context.Background(), claims)))
}

func TestProducerContextWithoutClaims(t *testing.T) {
	assert.False(t, producedby.Has(producerContext(context.Background(), nil)))
}

func TestNoteProducerRecordsTheWrite(t *testing.T) {
	rec := &recordingProducers{}
	noteProducer(context.Background(), Deps{Producers: rec}, &Claims{Sub: "sub-1"},
		producedby.Write{TargetID: "res-1", Created: true, Version: 1})
	require.Len(t, rec.writes, 1)
	assert.Equal(t, producedby.TargetResource, rec.writes[0].TargetKind)
	assert.Equal(t, "res-1", rec.writes[0].TargetID)
	assert.True(t, rec.writes[0].Created)
	assert.Equal(t, 1, rec.writes[0].Version)
	assert.Equal(t, "sub-1", rec.writes[0].Producer.ID)
}

func TestNoteProducerWithoutAStoreIsInert(t *testing.T) {
	assert.NotPanics(t, func() {
		noteProducer(context.Background(), Deps{}, &Claims{Sub: "sub-1"},
			producedby.Write{TargetID: "res-1", Version: 2})
	})
}

// TestCreateResourceRecordsItsProducer proves the note is taken at the create
// funnel itself, not only that the helper works: a resource created off HTTP
// carries a producer row marked as having created it.
func TestCreateResourceRecordsItsProducer(t *testing.T) {
	rec := &recordingProducers{}
	deps := Deps{Store: newMockStore(), S3Client: newMockS3(), S3Bucket: "b", Producers: rec}

	res, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())
	require.NoError(t, err)

	require.Len(t, rec.writes, 1)
	assert.Equal(t, producedby.TargetResource, rec.writes[0].TargetKind)
	assert.Equal(t, res.ID, rec.writes[0].TargetID)
	assert.True(t, rec.writes[0].Created)
	assert.Equal(t, 1, rec.writes[0].Version)
	assert.Equal(t, producedby.KindPerson, rec.writes[0].Producer.Kind)
	assert.Equal(t, "user-1", rec.writes[0].Producer.ID)
}

// TestCreateResourceUnderAScriptRecordsTheScript is acceptance criterion 2 at
// the write funnel: the run stamped its script id, and that is what is written
// even though the claims name only the principal.
func TestCreateResourceUnderAScriptRecordsTheScript(t *testing.T) {
	rec := &recordingProducers{}
	deps := Deps{Store: newMockStore(), S3Client: newMockS3(), S3Bucket: "b", Producers: rec}
	ctx := producedby.With(t.Context(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-1", Label: "daily-sales",
	})
	claims := &Claims{Sub: "script:daily-sales", Email: "owner@example.com", OnBehalfOf: "owner@example.com"}

	_, err := CreateResource(ctx, deps, claims, newResourceInput())
	require.NoError(t, err)

	require.Len(t, rec.writes, 1)
	assert.Equal(t, producedby.KindScript, rec.writes[0].Producer.Kind)
	assert.Equal(t, "script-1", rec.writes[0].Producer.ID)
	assert.Equal(t, "daily-sales", rec.writes[0].Producer.Label)
}

// TestReviseContentRecordsAModification is acceptance criterion 3 at the write
// funnel: replacing content a producer did not create records it as a modifier.
func TestReviseContentRecordsAModification(t *testing.T) {
	rec := &recordingProducers{}
	store, s3 := newMockStore(), newMockS3()
	deps := Deps{Store: store, S3Client: s3, S3Bucket: "b", Versions: newFakeVersions(store), Producers: rec}

	created, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())
	require.NoError(t, err)

	ctx := producedby.With(t.Context(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-1", Label: "daily-sales",
	})
	_, version, err := ReviseContent(ctx, deps, created,
		&Claims{Sub: "script:daily-sales", OnBehalfOf: "owner@example.com"},
		RevisionUpload{Data: []byte("day,high\ntue,72\n"), MIMEType: "text/csv"})
	require.NoError(t, err)

	require.Len(t, rec.writes, 2)
	revision := rec.writes[1]
	assert.False(t, revision.Created, "replacing content is not creating the file")
	assert.Equal(t, created.ID, revision.TargetID)
	assert.Equal(t, version.Version, revision.Version)
	assert.Equal(t, "script-1", revision.Producer.ID)
}
