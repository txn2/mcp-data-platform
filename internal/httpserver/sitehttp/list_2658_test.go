package sitehttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestList_QueriesAndPagesOnTheServer: q narrows the list with total counting
// every match, limit and offset page it, and a page or limit out of range is
// refused (#2658).
func TestList_QueriesAndPagesOnTheServer(t *testing.T) {
	f := newFixture(t)
	all := decodeBody[SiteList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, base, nil}, userOwner))
	require.NotEmpty(t, all.Sites)
	name := all.Sites[0].Name

	got := decodeBody[SiteList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, base + "?q=" + name, nil}, userOwner))
	assert.Equal(t, 1, got.Total)
	require.Len(t, got.Sites, 1)
	assert.Equal(t, name, got.Sites[0].Name)

	none := decodeBody[SiteList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, base + "?q=zz-no-such-site", nil}, userOwner))
	assert.Equal(t, 0, none.Total)
	assert.NotNil(t, none.Sites, "an empty list is [] (never null)")

	paged := decodeBody[SiteList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, base + "?limit=1&offset=50", nil}, userOwner))
	assert.Equal(t, all.Total, paged.Total)
	assert.Empty(t, paged.Sites)
	one := decodeBody[SiteList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, base + "?limit=1", nil}, userOwner))
	assert.Len(t, one.Sites, 1)

	for _, bad := range []string{"?limit=500", "?offset=-1", "?limit=x"} {
		f.statusIs(t, http.StatusBadRequest, route{http.MethodGet, base + bad, nil}, userOwner)
	}
}
