package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userdir "github.com/txn2/mcp-data-platform/pkg/user"
)

// fakeDirectoryStore is a minimal user.Store returning a fixed page.
type fakeDirectoryStore struct {
	users []userdir.User
	err   error
}

func (f *fakeDirectoryStore) List(context.Context, userdir.Filter) ([]userdir.User, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.users, len(f.users), nil
}
func (*fakeDirectoryStore) Observe(context.Context, string, string, string) error { return nil }
func (*fakeDirectoryStore) Insert(context.Context, userdir.User) error            { return nil }
func (*fakeDirectoryStore) Get(context.Context, string) (*userdir.User, error) {
	return nil, userdir.ErrNotFound
}
func (*fakeDirectoryStore) Update(context.Context, string, userdir.Update) error { return nil }
func (*fakeDirectoryStore) Delete(context.Context, string) error                 { return nil }

func TestListDirectoryUsers(t *testing.T) {
	t.Run("returns directory entries for an authenticated user", func(t *testing.T) {
		store := &fakeDirectoryStore{users: []userdir.User{
			{Email: "amy@example.com", FirstName: "Amy", LastName: "Adams", Confirmed: true},
			{Email: "bob@example.com", FirstName: "Bob", Confirmed: false},
		}}
		h := NewHandler(Deps{UserDirectory: store}, nil)

		req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/users", http.NoBody), "me@example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var resp directoryUsersResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 2, resp.Total)
		assert.Equal(t, "amy@example.com", resp.Users[0].Email)
		assert.True(t, resp.Users[0].Confirmed)
		assert.False(t, resp.Users[1].Confirmed)
	})

	t.Run("requires authentication", func(t *testing.T) {
		h := NewHandler(Deps{UserDirectory: &fakeDirectoryStore{}}, nil)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/users", http.NoBody)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("route absent when no directory wired", func(t *testing.T) {
		h := NewHandler(Deps{}, nil)
		req := withUser(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/users", http.NoBody), "me@example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
