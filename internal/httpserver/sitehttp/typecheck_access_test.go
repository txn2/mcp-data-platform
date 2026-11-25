package sitehttp

import (
	"net/http"
	"testing"
)

// TestTypechecks_OnlyManagersRead is #2793's access rule: a check's errors
// quote the release's source, so its owner and an administrator read them,
// and someone an environment is shared with, or a stranger, does not.
func TestTypechecks_OnlyManagersRead(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	for _, path := range []string{siteBase + "/typechecks", siteBase + "/releases/1/typecheck"} {
		f.statusIs(t, http.StatusOK, route{http.MethodGet, path, nil}, userOwner)
		f.statusIs(t, http.StatusOK, route{http.MethodGet, path, nil}, userAdmin)
		f.statusIs(t, http.StatusForbidden, route{http.MethodGet, path, nil}, userViewer)
		f.statusIs(t, http.StatusForbidden, route{http.MethodGet, path, nil}, userStranger)
		f.statusIs(t, http.StatusUnauthorized, route{http.MethodGet, path, nil}, nil)
	}
	f.statusIs(t, http.StatusForbidden, route{http.MethodPost, siteBase + "/releases/1/typecheck", nil}, userViewer)
}
