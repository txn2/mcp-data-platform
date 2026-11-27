//go:build integration

package sessionview

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionView_RealDB_SearchMatchesAnyWord is #2838's criterion for
// sessions, which rank by words alone: a question the session never stated
// whole finds it by the words it did state.
func TestSessionView_RealDB_SearchMatchesAnyWord(t *testing.T) {
	store, ctx := realDBFixture(t)

	got, err := store.Search(ctx, SearchQuery{Text: "how many orders shipped by region last quarter", UserID: realDBUser})
	require.NoError(t, err)
	require.Len(t, got, 1, "the session stated 'region', one word of the question")
	require.Equal(t, realDBSession, got[0].SessionID)
}
