package accessreq

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
)

// brokenRequests is a store whose every call fails.
type brokenRequests struct{ *memRequests }

var errDown = errors.New("store down")

func (brokenRequests) Open(context.Context, Request) (*Request, bool, error) {
	return nil, false, errDown
}

func (brokenRequests) ListForOwner(context.Context, string, string, string, bool) ([]Request, error) {
	return nil, errDown
}

func (brokenRequests) Decide(context.Context, string, Decision) error { return errDown }

// Every route refuses a caller with no session, and an item that does not
// exist is not found.
func TestRoutesNeedACallerAndAnItem(t *testing.T) {
	f := newFixture()
	for _, rt := range [][2]string{
		{http.MethodGet, "/api/v1/portal/assets/a1/access-request"},
		{http.MethodPost, "/api/v1/portal/assets/a1/access-requests"},
		{http.MethodPost, "/api/v1/portal/access-requests/r1/decline"},
	} {
		w, _ := f.do("", rt[0], rt[1], "")
		assert.Equal(t, http.StatusUnauthorized, w.Code, rt[1])
	}
	w, _ := f.do("reader", http.MethodGet, "/api/v1/portal/collections/missing/access-request", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	w, _ = f.do("owner", http.MethodPost, "/api/v1/portal/access-requests/none/decline", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	w, _ = f.do("reader", http.MethodPost, "/api/v1/portal/assets/a1/access-requests", `{"message":`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "a body that is not JSON")
	w, _ = f.do("owner", http.MethodPost, "/api/v1/portal/access-requests/none/approve", `{"permission":`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// A store that fails answers 500 without the error's text; a share that
// cannot be created leaves the request pending.
func TestRoutesWhenTheStoreFails(t *testing.T) {
	f := newFixture()
	_, req := f.do("reader", http.MethodPost, "/api/v1/portal/assets/a1/access-requests", "")
	id, _ := req["id"].(string)

	broken := newFixture()
	broken.store.rows = f.store.rows
	mux := http.NewServeMux()
	Register(mux, func(h http.Handler) http.Handler { return h }, Deps{
		Store:   brokenRequests{broken.store},
		Resolve: func(context.Context, string, string) (Target, error) { return Target{OwnerID: "owner"}, nil },
		CanOpen: func(context.Context, *access.User, string, string) bool { return false },
		IsAdmin: func(*access.User) bool { return false },
		Share: func(context.Context, string, string, string, string, string) (string, error) {
			return "", errDown
		},
	})
	broken.mux = mux
	for _, rt := range [][3]string{
		{http.MethodPost, "/api/v1/portal/assets/a2/access-requests", ""},
		{http.MethodGet, "/api/v1/portal/access-requests", ""},
		{http.MethodPost, "/api/v1/portal/access-requests/" + id + "/decline", ""},
		{http.MethodPost, "/api/v1/portal/access-requests/" + id + "/approve", `{"permission":"viewer"}`},
	} {
		w, _ := broken.do("owner", rt[0], rt[1], rt[2])
		assert.Equal(t, http.StatusInternalServerError, w.Code, rt[1])
		assert.NotContains(t, w.Body.String(), errDown.Error(), rt[1])
	}
	pending, err := f.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, pending.Status)
}

// A body larger than the message limit is refused before it is read whole.
func TestOpenRefusesAnOversizedBody(t *testing.T) {
	f := newFixture()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/assets/a1/access-requests",
		strings.NewReader(`{"message":"`+strings.Repeat("x", maxBody)+`"}`))
	r = r.WithContext(access.ContextWithUser(r.Context(), &access.User{UserID: "reader", Email: "reader@example.com"}))
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
