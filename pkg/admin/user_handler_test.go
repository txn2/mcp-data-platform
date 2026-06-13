package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/user"
)

// fakeUserStore is an in-memory user.Store for handler tests.
type fakeUserStore struct {
	mu    sync.Mutex
	users map[string]user.User
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{users: make(map[string]user.User)}
}

func (*fakeUserStore) Observe(context.Context, string, string, string) error { return nil }

func (f *fakeUserStore) Insert(_ context.Context, u user.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.Email]; ok {
		return user.ErrAlreadyExists
	}
	f.users[u.Email] = u
	return nil
}

func (f *fakeUserStore) Get(_ context.Context, email string) (*user.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &u, nil
}

func (f *fakeUserStore) List(_ context.Context, filter user.Filter) ([]user.User, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []user.User
	for _, u := range f.users {
		if filter.Query == "" || strings.Contains(u.Email, filter.Query) {
			out = append(out, u)
		}
	}
	return out, len(out), nil
}

func (f *fakeUserStore) Update(_ context.Context, email string, upd user.Update) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[email]
	if !ok {
		return user.ErrNotFound
	}
	if upd.FirstName != nil {
		u.FirstName = *upd.FirstName
	}
	if upd.LastName != nil {
		u.LastName = *upd.LastName
	}
	f.users[email] = u
	return nil
}

func (f *fakeUserStore) Delete(_ context.Context, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[email]; !ok {
		return user.ErrNotFound
	}
	delete(f.users, email)
	return nil
}

func newUserHandler(store user.Store) *Handler {
	return NewHandler(Deps{
		UserStore:       store,
		PersonaRegistry: &mockPersonaRegistry{},
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
}

func doUserReq(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCreateUser(t *testing.T) {
	t.Run("adds a user", func(t *testing.T) {
		store := newFakeUserStore()
		h := newUserHandler(store)

		w := doUserReq(t, h, http.MethodPost, "/api/v1/admin/users",
			`{"email":"Marcus.Johnson@Example.com","first_name":"Marcus","last_name":"Johnson"}`)

		require.Equal(t, http.StatusCreated, w.Code)
		var resp userSummary
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "marcus.johnson@example.com", resp.Email)
		assert.Equal(t, user.SourceAdmin, resp.Source)
		assert.False(t, resp.Confirmed)
	})

	t.Run("rejects invalid email", func(t *testing.T) {
		h := newUserHandler(newFakeUserStore())
		w := doUserReq(t, h, http.MethodPost, "/api/v1/admin/users", `{"email":"not-an-email"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects duplicate", func(t *testing.T) {
		store := newFakeUserStore()
		_ = store.Insert(context.Background(), user.User{Email: "dup@example.com"})
		h := newUserHandler(store)
		w := doUserReq(t, h, http.MethodPost, "/api/v1/admin/users", `{"email":"dup@example.com"}`)
		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("rejects over-long name", func(t *testing.T) {
		h := newUserHandler(newFakeUserStore())
		long := strings.Repeat("x", user.MaxNameLen+1)
		w := doUserReq(t, h, http.MethodPost, "/api/v1/admin/users",
			`{"email":"a@b.io","first_name":"`+long+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetUserHandler(t *testing.T) {
	store := newFakeUserStore()
	_ = store.Insert(context.Background(), user.User{Email: "a@b.io", FirstName: "Amy"})
	h := newUserHandler(store)

	w := doUserReq(t, h, http.MethodGet, "/api/v1/admin/users/a@b.io", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp userSummary
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Amy", resp.FirstName)

	w = doUserReq(t, h, http.MethodGet, "/api/v1/admin/users/missing@b.io", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateUser(t *testing.T) {
	store := newFakeUserStore()
	_ = store.Insert(context.Background(), user.User{Email: "a@b.io", FirstName: "Amy", LastName: "Adams"})
	h := newUserHandler(store)

	w := doUserReq(t, h, http.MethodPut, "/api/v1/admin/users/a@b.io", `{"last_name":"Adamson"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp userSummary
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Adamson", resp.LastName)
	assert.Equal(t, "Amy", resp.FirstName)

	w = doUserReq(t, h, http.MethodPut, "/api/v1/admin/users/missing@b.io", `{"last_name":"X"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateUser_SanitizesFirstName(t *testing.T) {
	store := newFakeUserStore()
	_ = store.Insert(context.Background(), user.User{Email: "a@b.io", FirstName: "Amy", LastName: "Adams"})
	h := newUserHandler(store)

	// First name carries a control character; the update must strip it and the
	// FirstName branch of sanitizedUpdate must run.
	w := doUserReq(t, h, http.MethodPut, "/api/v1/admin/users/a@b.io", "{\"first_name\":\"Mar\\ncus\"}")
	require.Equal(t, http.StatusOK, w.Code)
	var resp userSummary
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Marcus", resp.FirstName)
	assert.Equal(t, "Adams", resp.LastName)
}

func TestCreateUser_SanitizesNames(t *testing.T) {
	store := newFakeUserStore()
	h := newUserHandler(store)

	w := doUserReq(t, h, http.MethodPost, "/api/v1/admin/users",
		"{\"email\":\"a@b.io\",\"first_name\":\"Mar\\ncus\",\"last_name\":\"John\\tson\"}")
	require.Equal(t, http.StatusCreated, w.Code)
	var resp userSummary
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "Marcus", resp.FirstName)
	assert.Equal(t, "Johnson", resp.LastName)
}

func TestListUsers(t *testing.T) {
	store := newFakeUserStore()
	_ = store.Insert(context.Background(), user.User{Email: "a@b.io"})
	_ = store.Insert(context.Background(), user.User{Email: "c@d.io"})
	h := newUserHandler(store)

	w := doUserReq(t, h, http.MethodGet, "/api/v1/admin/users", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp userListResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 2, resp.Total)
}

func TestDeleteUser(t *testing.T) {
	store := newFakeUserStore()
	_ = store.Insert(context.Background(), user.User{Email: "a@b.io"})
	h := newUserHandler(store)

	w := doUserReq(t, h, http.MethodDelete, "/api/v1/admin/users/a@b.io", "")
	assert.Equal(t, http.StatusOK, w.Code)

	w = doUserReq(t, h, http.MethodDelete, "/api/v1/admin/users/a@b.io", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// errUserStore returns errSentinel from every operation to exercise 500 paths.
type errUserStore struct{ err error }

func (e *errUserStore) Observe(context.Context, string, string, string) error { return e.err }
func (e *errUserStore) Insert(context.Context, user.User) error               { return e.err }
func (e *errUserStore) Get(context.Context, string) (*user.User, error)       { return nil, e.err }
func (e *errUserStore) List(context.Context, user.Filter) ([]user.User, int, error) {
	return nil, 0, e.err
}
func (e *errUserStore) Update(context.Context, string, user.Update) error { return e.err }
func (e *errUserStore) Delete(context.Context, string) error              { return e.err }

func TestUserHandlers_StoreErrors(t *testing.T) {
	boom := errors.New("boom")
	h := newUserHandler(&errUserStore{err: boom})

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/admin/users", ""},
		{http.MethodGet, "/api/v1/admin/users/a@b.io", ""},
		{http.MethodPost, "/api/v1/admin/users", `{"email":"a@b.io"}`},
		{http.MethodPut, "/api/v1/admin/users/a@b.io", `{"last_name":"X"}`},
		{http.MethodDelete, "/api/v1/admin/users/a@b.io", ""},
	}
	for _, c := range cases {
		w := doUserReq(t, h, c.method, c.path, c.body)
		assert.Equal(t, http.StatusInternalServerError, w.Code, "%s %s", c.method, c.path)
	}
}

func TestUserHandlers_BadInput(t *testing.T) {
	h := newUserHandler(newFakeUserStore())

	// Invalid email in the path is rejected with 400 before the store is hit.
	for _, m := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"last_name":"X"}`},
		{http.MethodDelete, ""},
	} {
		w := doUserReq(t, h, m.method, "/api/v1/admin/users/not-an-email", m.body)
		assert.Equal(t, http.StatusBadRequest, w.Code, m.method)
	}

	// Malformed JSON bodies.
	assert.Equal(t, http.StatusBadRequest,
		doUserReq(t, h, http.MethodPost, "/api/v1/admin/users", `{`).Code)
	assert.Equal(t, http.StatusBadRequest,
		doUserReq(t, h, http.MethodPut, "/api/v1/admin/users/a@b.io", `{`).Code)

	// Over-long name on update.
	long := strings.Repeat("x", user.MaxNameLen+1)
	assert.Equal(t, http.StatusBadRequest,
		doUserReq(t, h, http.MethodPut, "/api/v1/admin/users/a@b.io", `{"first_name":"`+long+`"}`).Code)

	// Bad pagination params fall back to defaults (still 200).
	store := newFakeUserStore()
	_ = store.Insert(context.Background(), user.User{Email: "a@b.io"})
	assert.Equal(t, http.StatusOK,
		doUserReq(t, newUserHandler(store), http.MethodGet, "/api/v1/admin/users?limit=abc&offset=-3", "").Code)
}

func TestUserRoutes_ReadOnlyInFileMode(t *testing.T) {
	h := NewHandler(Deps{
		UserStore:       newFakeUserStore(),
		PersonaRegistry: &mockPersonaRegistry{},
		ConfigStore:     &mockConfigStore{mode: "file"},
	}, nil)

	w := doUserReq(t, h, http.MethodPost, "/api/v1/admin/users", `{"email":"a@b.io"}`)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
