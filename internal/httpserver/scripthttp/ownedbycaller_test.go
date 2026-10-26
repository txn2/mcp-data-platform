package scripthttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// OwnedByCaller is the ownership gate a route package mounted beside this
// one checks with (#2180): nobody signed in is answered 401.
func TestOwnedByCaller_RefusesNobody(t *testing.T) {
	h := New(Deps{PortalUser: func(*http.Request) *PortalIdentity { return nil }})
	rec := httptest.NewRecorder()
	_, _, ok := h.OwnedByCaller(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	assert.False(t, ok)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
