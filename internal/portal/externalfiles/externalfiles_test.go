package externalfiles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

var errDown = errors.New("database down")

func mockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return NewStore(db), mock
}

func TestStorePeople(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectQuery(peopleQuery).WillReturnRows(sqlmock.NewRows([]string{"email", "first", "last", "roles", "subject"}).
		AddRow("dana@client-a.example.com", "Dana", "Reyes", []byte(`["client-a"]`), "sub-dana").
		AddRow("ann@example.com", "", "", []byte(`[]`), ""))
	people, err := s.People(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Person{
		{Email: "dana@client-a.example.com", FirstName: "Dana", LastName: "Reyes", Roles: []string{"client-a"}, Subject: "sub-dana"},
		{Email: "ann@example.com", Roles: []string{}},
	}, people)

	mock.ExpectQuery(peopleQuery).WillReturnError(errDown)
	_, err = s.People(context.Background())
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery(peopleQuery).WillReturnRows(sqlmock.NewRows([]string{"email", "first", "last", "roles", "subject"}).
		AddRow("x", "", "", []byte(`not json`), ""))
	_, err = s.People(context.Background())
	require.Error(t, err)

	mock.ExpectQuery(peopleQuery).WillReturnRows(sqlmock.NewRows([]string{"email"}).AddRow("x"))
	_, err = s.People(context.Background())
	require.Error(t, err)

	mock.ExpectQuery(peopleQuery).WillReturnRows(sqlmock.NewRows([]string{"email", "first", "last", "roles", "subject"}).
		AddRow("x", "", "", []byte(`[]`), "").RowError(0, errDown))
	_, err = s.People(context.Background())
	require.ErrorIs(t, err, errDown)
}

func TestStoreRolesOf(t *testing.T) {
	s, mock := mockStore(t)
	mock.ExpectQuery(rolesQuery).WithArgs("sub-dana").
		WillReturnRows(sqlmock.NewRows([]string{"roles"}).AddRow([]byte(`["client-a"]`)))
	sets, err := s.RolesOf(context.Background(), "sub-dana")
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"client-a"}}, sets)

	mock.ExpectQuery(rolesQuery).WillReturnError(errDown)
	_, err = s.RolesOf(context.Background(), "x")
	require.ErrorIs(t, err, errDown)

	mock.ExpectQuery(rolesQuery).WillReturnRows(sqlmock.NewRows([]string{"roles"}).AddRow([]byte(`{`)))
	_, err = s.RolesOf(context.Background(), "x")
	require.Error(t, err)

	mock.ExpectQuery(rolesQuery).WillReturnRows(sqlmock.NewRows([]string{"roles", "extra"}).AddRow([]byte(`[]`), 1))
	_, err = s.RolesOf(context.Background(), "x")
	require.Error(t, err)

	mock.ExpectQuery(rolesQuery).WillReturnRows(sqlmock.NewRows([]string{"roles"}).AddRow([]byte(`[]`)).RowError(0, errDown))
	_, err = s.RolesOf(context.Background(), "x")
	require.ErrorIs(t, err, errDown)
}

// memSource is the directory as two clients and one staff member hold it.
type memSource struct {
	people []Person
	err    error
}

func (m memSource) People(context.Context) ([]Person, error) { return m.people, m.err }

func (m memSource) RolesOf(_ context.Context, id string) ([][]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := [][]string{}
	for _, p := range m.people {
		if p.Email == id || (p.Subject != "" && p.Subject == id) {
			out = append(out, p.Roles)
		}
	}
	return out, nil
}

var directory = memSource{people: []Person{
	{Email: "dana@client-a.example.com", FirstName: "Dana", LastName: "Reyes", Roles: []string{"client-a"}, Subject: "sub-dana"},
	{Email: "lee@client-b.example.com", Roles: []string{"client-b"}},
	{Email: "ann@example.com", Roles: []string{"staff"}, Subject: "sub-ann"},
}}

func registry(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	for _, p := range []*persona.Persona{
		{Name: "client-a", DisplayName: "Client A", Roles: []string{"client-a"}, External: true},
		{Name: "client-b", DisplayName: "Client B", Roles: []string{"client-b"}, External: true},
		{Name: "staff", Roles: []string{"staff"}, Portal: &persona.PortalAccess{ExternalFiles: persona.PortalView}},
		{Name: "leads", Roles: []string{"lead"}, Portal: &persona.PortalAccess{ExternalFiles: persona.PortalManage}},
		{Name: "others", Roles: []string{"other"}},
	} {
		require.NoError(t, reg.Register(p))
	}
	return reg
}

func TestLevel(t *testing.T) {
	svc := New(directory, registry(t).GetForRoles)
	assert.Equal(t, persona.PortalNone, svc.Level(nil))
	assert.Equal(t, persona.PortalManage, svc.Level(&resource.Claims{IsAdmin: true}))
	assert.Equal(t, persona.PortalView, svc.Level(&resource.Claims{Roles: []string{"staff"}}))
	assert.Equal(t, persona.PortalManage, svc.Level(&resource.Claims{Roles: []string{"lead"}}))
	assert.Equal(t, persona.PortalNone, svc.Level(&resource.Claims{Roles: []string{"other"}}))
	assert.Equal(t, persona.PortalNone, svc.Level(&resource.Claims{Roles: []string{"client-a"}}))
	assert.Equal(t, persona.PortalNone, svc.Level(&resource.Claims{Roles: []string{"nobody"}}))
}

func bindAs(svc *Service, roles []string) resource.ClaimsExtractor {
	return svc.Bind(func(*http.Request) (*resource.Claims, error) {
		return &resource.Claims{Sub: "me", Email: "me@example.com", Roles: roles}, nil
	})
}

func TestBind(t *testing.T) {
	svc := New(directory, registry(t).GetForRoles)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)

	c, err := bindAs(svc, []string{"staff"})(req)
	require.NoError(t, err)
	require.NotNil(t, c.ExternalFolder)
	assert.False(t, c.ManageExternalFiles)
	assert.True(t, c.ExternalFolder("sub-dana"), "by subject")
	assert.True(t, c.ExternalFolder("sub-dana"), "read once, answered again")
	assert.True(t, c.ExternalFolder("lee@client-b.example.com"), "by address")
	assert.False(t, c.ExternalFolder("sub-ann"), "a staff member's folder")
	assert.False(t, c.ExternalFolder("unknown"))

	c, err = bindAs(svc, []string{"lead"})(req)
	require.NoError(t, err)
	assert.True(t, c.ManageExternalFiles)

	c, err = bindAs(svc, []string{"other"})(req)
	require.NoError(t, err)
	assert.Nil(t, c.ExternalFolder, "no level, no reach")

	failing := svc.Bind(func(*http.Request) (*resource.Claims, error) { return nil, errDown })
	_, err = failing(req)
	require.ErrorIs(t, err, errDown)

	down := New(memSource{err: errDown}, registry(t).GetForRoles)
	c, err = bindAs(down, []string{"staff"})(req)
	require.NoError(t, err)
	assert.False(t, c.ExternalFolder("sub-dana"), "a read that fails reaches nothing")
}

func serve(svc *Service, extract resource.ClaimsExtractor) *httptest.ResponseRecorder {
	svc.Bind(extract)
	mux := http.NewServeMux()
	svc.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, Path, http.NoBody))
	return rec
}

func as(roles ...string) resource.ClaimsExtractor {
	return func(*http.Request) (*resource.Claims, error) { return &resource.Claims{Sub: "me", Roles: roles}, nil }
}

func TestListFolders(t *testing.T) {
	reg := registry(t)
	rec := serve(New(directory, reg.GetForRoles), as("staff"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got folderList
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.Manage)
	assert.Equal(t, []Folder{
		{ScopeID: "sub-dana", Email: "dana@client-a.example.com", Name: "Dana Reyes", Persona: "client-a", PersonaDisplayName: "Client A"},
		{ScopeID: "lee@client-b.example.com", Email: "lee@client-b.example.com", Persona: "client-b", PersonaDisplayName: "Client B"},
	}, got.Folders)

	rec = serve(New(directory, reg.GetForRoles), as("lead"))
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.True(t, got.Manage)

	rec = serve(New(memSource{people: []Person{}}, reg.GetForRoles), as("staff"))
	assert.JSONEq(t, `{"folders":[],"manage":false}`, rec.Body.String(), "no external people is an empty list")

	assert.Equal(t, http.StatusForbidden, serve(New(directory, reg.GetForRoles), as("other")).Code)
	assert.Equal(t, http.StatusForbidden, serve(New(directory, reg.GetForRoles), as("client-a")).Code)
	assert.Equal(t, http.StatusInternalServerError, serve(New(memSource{err: errDown}, reg.GetForRoles), as("staff")).Code)
	assert.Equal(t, http.StatusUnauthorized, serve(New(directory, reg.GetForRoles), func(*http.Request) (*resource.Claims, error) {
		return nil, errDown
	}).Code)
	assert.Equal(t, http.StatusForbidden, serve(New(directory, reg.GetForRoles), func(*http.Request) (*resource.Claims, error) {
		return nil, resource.ErrForbidden
	}).Code, "a refused CSRF token is forbidden, not signed out")
}
