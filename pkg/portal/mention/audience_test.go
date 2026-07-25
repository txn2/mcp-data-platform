package mention

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockAudience(t *testing.T) (*Audience, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return NewAudience(db), mock, func() { _ = db.Close() }
}

func TestAudienceList_AssetJoinsDirectoryAndExcludesCaller(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").
		WithArgs("asset_1", "%mar%", "me@example.com", 20).
		WillReturnRows(sqlmock.NewRows([]string{"email", "first_name", "last_name", "confirmed"}).
			AddRow("marcus.johnson@example.com", "Marcus", "Johnson", true).
			AddRow("marge@example.com", "", "", false))

	people, err := audience.List(context.Background(),
		Target{Type: TargetAsset, ID: "asset_1"},
		ListOptions{Query: "MAR", Exclude: "Me@Example.com"})
	require.NoError(t, err)
	assert.Equal(t, []Person{
		{Email: "marcus.johnson@example.com", FirstName: "Marcus", LastName: "Johnson", Confirmed: true},
		{Email: "marge@example.com", Confirmed: false},
	}, people)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAudienceList_ClampsPageSize(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_collections").
		WithArgs("col_1", "%%", "", maxListLimit).
		WillReturnRows(sqlmock.NewRows([]string{"email", "first_name", "last_name", "confirmed"}))

	_, err := audience.List(context.Background(),
		Target{Type: TargetCollection, ID: "col_1"}, ListOptions{Limit: maxListLimit + 500})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAudienceList_OpenTargetReadsTheDirectory(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	// No target id is bound: an open target's audience is every known user.
	mock.ExpectQuery("FROM users").
		WithArgs("%%", "", defaultListLimit).
		WillReturnRows(sqlmock.NewRows([]string{"email", "first_name", "last_name", "confirmed"}).
			AddRow("anyone@example.com", "", "", true))

	people, err := audience.List(context.Background(),
		Target{Type: TargetKnowledgePage, ID: "kp_1"}, ListOptions{})
	require.NoError(t, err)
	require.Len(t, people, 1)
	assert.Equal(t, "anyone@example.com", people[0].Email)
}

func TestAudienceList_UnknownTarget(t *testing.T) {
	audience, _, done := newMockAudience(t)
	defer done()

	_, err := audience.List(context.Background(), Target{Type: "dataset", ID: "x"}, ListOptions{})
	assert.ErrorIs(t, err, ErrUnknownTarget)
}

func TestAudienceEligible_KeepsOnlyMembersInWrittenOrder(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").
		WillReturnRows(sqlmock.NewRows([]string{"email"}).
			AddRow("bob@example.com").
			AddRow("alice@example.com"))

	got, err := audience.Eligible(context.Background(),
		Target{Type: TargetAsset, ID: "asset_1"},
		[]string{"Alice@example.com", "stranger@example.com", "bob@example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"alice@example.com", "bob@example.com"}, got,
		"a named address outside the audience is dropped; the rest keep the order they were written in")
}

func TestAudienceEligible_NoNamesSkipsTheQuery(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	got, err := audience.Eligible(context.Background(), Target{Type: TargetAsset, ID: "a"}, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet(), "no query runs when nobody was named")
}

func TestAudienceEligible_QueryError(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").WillReturnError(errors.New("boom"))

	_, err := audience.Eligible(context.Background(),
		Target{Type: TargetAsset, ID: "a"}, []string{"bob@example.com"})
	assert.Error(t, err)
}

func TestAudienceEligible_PromptScopeDecidesTheAudience(t *testing.T) {
	tests := []struct {
		name       string
		scope      string
		wantSource string
	}{
		{name: "personal prompt is owner and shares", scope: "personal", wantSource: "FROM prompts"},
		{name: "global prompt is visible to everyone", scope: "global", wantSource: "FROM users"},
		{name: "persona prompt is visible to everyone", scope: "persona", wantSource: "FROM users"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			audience, mock, done := newMockAudience(t)
			defer done()

			mock.ExpectQuery("SELECT scope FROM prompts").
				WithArgs("prm_1").
				WillReturnRows(sqlmock.NewRows([]string{"scope"}).AddRow(tt.scope))
			mock.ExpectQuery(tt.wantSource).
				WillReturnRows(sqlmock.NewRows([]string{"email"}).AddRow("bob@example.com"))

			got, err := audience.Eligible(context.Background(),
				Target{Type: TargetPrompt, ID: "prm_1"}, []string{"bob@example.com"})
			require.NoError(t, err)
			assert.Equal(t, []string{"bob@example.com"}, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAudienceEligible_MissingPromptMatchesNobody(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("SELECT scope FROM prompts").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("FROM prompts").
		WillReturnRows(sqlmock.NewRows([]string{"email"}))

	got, err := audience.Eligible(context.Background(),
		Target{Type: TargetPrompt, ID: "gone"}, []string{"bob@example.com"})
	require.NoError(t, err)
	assert.Empty(t, got, "a deleted prompt must not resolve to the whole directory")
}

func TestAudienceEligible_PromptScopeLookupError(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("SELECT scope FROM prompts").WillReturnError(errors.New("boom"))

	_, err := audience.Eligible(context.Background(),
		Target{Type: TargetPrompt, ID: "prm_1"}, []string{"bob@example.com"})
	assert.Error(t, err)
}

func TestGrantees(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").
		WithArgs("asset_1").
		WillReturnRows(sqlmock.NewRows([]string{"email"}).
			AddRow("owner@example.com").
			AddRow("shared@example.com"))

	got, err := audience.Grantees(context.Background(), TargetAsset, "asset_1")
	require.NoError(t, err)
	assert.Equal(t, []string{"owner@example.com", "shared@example.com"}, got)
}

// A knowledge page is readable by everyone, but a comment on one must not mail
// the entire directory: it has no grantees, so the fan-out stays with the owner
// and the thread author the notifier already knows.
func TestGrantees_OpenTargetHasNone(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	for _, targetType := range []string{TargetKnowledgePage, TargetStandalone, "unknown"} {
		got, err := audience.Grantees(context.Background(), targetType, "id")
		require.NoError(t, err)
		assert.Nil(t, got, "target type %q", targetType)
	}
	assert.NoError(t, mock.ExpectationsWereMet(), "no query runs for a target with no grants")
}

func TestGrantees_QueryError(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_collections").WillReturnError(errors.New("boom"))

	_, err := audience.Grantees(context.Background(), TargetCollection, "col_1")
	assert.Error(t, err)
}

// A "%" or "_" typed into the picker must match itself: the picker chooses who
// receives an email, so it must never quietly widen to people the author did
// not search for.
func TestAudienceList_EscapesLikeWildcards(t *testing.T) {
	audience, mock, done := newMockAudience(t)
	defer done()

	mock.ExpectQuery("FROM portal_assets").
		WithArgs("asset_1", `%first\_last50\%%`, "", defaultListLimit).
		WillReturnRows(sqlmock.NewRows([]string{"email", "first_name", "last_name", "confirmed"}))

	_, err := audience.List(context.Background(),
		Target{Type: TargetAsset, ID: "asset_1"}, ListOptions{Query: "first_last50%"})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEscapeLike(t *testing.T) {
	assert.Equal(t, `plain`, escapeLike("plain"))
	assert.Equal(t, `a\%b`, escapeLike("a%b"))
	assert.Equal(t, `a\_b`, escapeLike("a_b"))
	assert.Equal(t, `a\\b`, escapeLike(`a\b`))
}
