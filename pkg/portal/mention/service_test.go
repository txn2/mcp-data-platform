package mention

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceResolveMentions_KeepsOnlyTheAudience(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(sqlmock.NewRows([]string{"email"}).AddRow("bob@example.com"))

	got := NewService(audience).ResolveMentions(context.Background(), TargetAsset, "asset_1",
		"@bob(example.com) please review, cc @stranger(example.com)", "author@example.com")
	assert.Equal(t, []string{"bob@example.com"}, got,
		"a name outside the audience is left as plain text and delivers nothing")
}

func TestServiceResolveMentions_NoTokensSkipsTheLookup(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	got := NewService(audience).ResolveMentions(context.Background(), TargetAsset, "asset_1", "looks good", "author@example.com")
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet(), "a body naming nobody must not query the audience")
}

// An audience lookup failure must never fail the comment it serves: the write
// proceeds with the tokens left as text.
func TestServiceResolveMentions_LookupFailureYieldsNoMentions(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").WillReturnError(errors.New("database down"))

	got := NewService(audience).ResolveMentions(context.Background(), TargetAsset, "asset_1",
		"@bob(example.com) ping", "author@example.com")
	assert.Nil(t, got)
}

func TestServiceResolveMentions_UnavailableService(t *testing.T) {
	var nilService *Service
	assert.Nil(t, nilService.ResolveMentions(context.Background(), TargetAsset, "a", "@bob(example.com)", "author@example.com"))
	assert.Nil(t, NewService(nil).ResolveMentions(context.Background(), TargetAsset, "a", "@bob(example.com)", "author@example.com"))
}

func TestWithMentions(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		emails   []string
		want     string
	}{
		{name: "empty metadata", metadata: "", emails: []string{"bob@example.com"}, want: `{"mentions":["bob@example.com"]}`},
		{
			name:     "preserves existing keys",
			metadata: `{"anchor":"p3"}`,
			emails:   []string{"bob@example.com"},
			want:     `{"anchor":"p3","mentions":["bob@example.com"]}`,
		},
		{name: "no mentions leaves metadata untouched", metadata: `{"anchor":"p3"}`, emails: nil, want: `{"anchor":"p3"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WithMentions(json.RawMessage(tt.metadata), tt.emails)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestWithMentions_MalformedMetadata(t *testing.T) {
	_, err := WithMentions(json.RawMessage("not json"), []string{"bob@example.com"})
	assert.Error(t, err)
}

func TestFromMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     []string
	}{
		{name: "records the stored list", metadata: `{"mentions":["bob@example.com"]}`, want: []string{"bob@example.com"}},
		{name: "absent key", metadata: `{"anchor":"p3"}`, want: nil},
		{name: "empty", metadata: "", want: nil},
		{name: "malformed renders as none", metadata: "not json", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FromMetadata(json.RawMessage(tt.metadata)))
		})
	}
}

// The stored shape and the query that finds it must agree: what WithMentions
// writes has to satisfy the containment document ContainmentFilter builds.
func TestContainmentFilterMatchesWhatWithMentionsStores(t *testing.T) {
	stored, err := WithMentions(nil, []string{"bob@example.com", "alice@example.com"})
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(stored, &fields))
	var mentions []string
	require.NoError(t, json.Unmarshal(fields[MetadataKey], &mentions))

	var wanted []string
	require.NoError(t, json.Unmarshal([]byte(ContainmentFilter("Bob@Example.com")), &wanted))
	require.Len(t, wanted, 1)
	assert.Contains(t, mentions, wanted[0],
		"the filter address must appear in the stored array for jsonb containment to match")
}

// The author's own address is dropped before the audience lookup: a
// self-mention notifies nobody, so recording it would put the thread in their
// own inbox and render a chip for a delivery that never happened.
func TestServiceResolveMentions_DropsTheAuthorsOwnAddress(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	got := NewService(audience).ResolveMentions(context.Background(), TargetAsset, "asset_1",
		"@me(example.com) note to self", "ME@example.com")
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet(), "a body naming only the author must not query the audience")
}
