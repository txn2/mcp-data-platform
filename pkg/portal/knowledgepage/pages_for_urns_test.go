package knowledgepage

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReverseLooker struct {
	byKey map[string][]PageRef
	err   error
}

func (f *fakeReverseLooker) ListPagesReferencing(_ context.Context, ref EntityRef) ([]PageRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	if ref.TargetType == RefTargetDataHub {
		return f.byKey[ref.EntityURN], nil
	}
	return f.byKey[ref.ConnectionKind+"/"+ref.ConnectionName], nil
}

func TestPagesForURNs_DedupAcrossURNsAndSkipsUnparseable(t *testing.T) {
	dataset := "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"
	f := &fakeReverseLooker{byKey: map[string][]PageRef{
		dataset:      {{ID: "kp1", Title: "A"}, {ID: "kp2", Title: "B"}},
		"trino/acme": {{ID: "kp2", Title: "B"}, {ID: "kp3", Title: "C"}}, // kp2 repeats across URNs
	}}
	urns := []string{dataset, "mcp:connection:(trino,acme)", "not-a-ref"}

	pages, err := PagesForURNs(context.Background(), f, urns, 0)
	require.NoError(t, err)
	require.Len(t, pages, 3, "kp2 deduped, the unparseable urn skipped")
	assert.Equal(t, []string{"kp1", "kp2", "kp3"}, []string{pages[0].ID, pages[1].ID, pages[2].ID})
}

func TestPagesForURNs_CapAndErrorPropagation(t *testing.T) {
	dataset := "urn:li:dataset:(urn:li:dataPlatform:trino,a.b.c,PROD)"
	f := &fakeReverseLooker{byKey: map[string][]PageRef{
		dataset: {{ID: "kp1"}, {ID: "kp2"}, {ID: "kp3"}},
	}}
	pages, err := PagesForURNs(context.Background(), f, []string{dataset}, 2)
	require.NoError(t, err)
	assert.Len(t, pages, 2, "result is capped at limit")

	_, err = PagesForURNs(context.Background(), &fakeReverseLooker{err: errors.New("boom")}, []string{dataset}, 0)
	assert.Error(t, err, "a lookup error propagates")
}
