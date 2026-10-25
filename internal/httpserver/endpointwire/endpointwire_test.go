package endpointwire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

type authFunc func(r *http.Request) (*portal.User, error)

func (f authFunc) Authenticate(r *http.Request) (*portal.User, error) { return f(r) }

func TestAuthenticator(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/endpoints/x", http.NoBody)

	caller, err := Authenticator(authFunc(func(*http.Request) (*portal.User, error) {
		return &portal.User{UserID: "u1", Email: "ann@example.com", AuthType: "apikey", Roles: []string{"analyst"}}, nil
	}), nil)(req)
	require.NoError(t, err)
	assert.Equal(t, "ann@example.com", caller.Email)
	assert.Equal(t, "apikey", caller.AuthType)

	caller, err = Authenticator(authFunc(func(*http.Request) (*portal.User, error) { return nil, nil }), nil)(req) //nolint:nilnil // the portal's "no credential" answer
	require.NoError(t, err)
	assert.Nil(t, caller, "no credential is nobody")

	_, err = Authenticator(authFunc(func(*http.Request) (*portal.User, error) { return nil, errors.New("csrf") }), nil)(req)
	require.ErrorIs(t, err, ephttp.ErrForbidden, "a session refused its CSRF token is a refused credential")
}

// A person of an external persona reaches no endpoint, signed in or not
// (#2290); an internal person is let through.
func TestAuthenticatorRefusesExternalPeople(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/endpoints/x", http.NoBody)
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "client-a", Roles: []string{"client-a"}, External: true}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "staff", Roles: []string{"staff"}}))
	as := func(role string) authFunc {
		return func(*http.Request) (*portal.User, error) {
			return &portal.User{UserID: "u", Email: "p@example.com", Roles: []string{role}}, nil
		}
	}
	_, err := Authenticator(as("client-a"), reg.IsExternalRoles)(req)
	require.ErrorIs(t, err, ephttp.ErrForbidden)
	caller, err := Authenticator(as("staff"), reg.IsExternalRoles)(req)
	require.NoError(t, err)
	assert.Equal(t, "p@example.com", caller.Email)
}

func TestMount_NoDatabaseMountsNothing(t *testing.T) {
	p, err := platform.New(platform.WithConfig(&platform.Config{Server: platform.ServerConfig{Name: "test"}}))
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	mux := http.NewServeMux()
	Mount(mux, p, nil, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/endpoints/x", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMount_TheEndpointAndSettingsRoutes(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	e := epwire.Build(epwire.Deps{DB: db})
	nobody := authFunc(func(*http.Request) (*portal.User, error) { return nil, nil }) //nolint:nilnil // the portal's "no credential" answer

	mux := http.NewServeMux()
	mountOver(mux, e, Authenticator(nobody, nil), nil, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/botcheck/solver.js", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code, "the bot check's script is served")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/settings/bot-check", http.NoBody))
	assert.Equal(t, http.StatusNotFound, rec.Code, "no settings routes without the admin API")

	mux = http.NewServeMux()
	refuse := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	}
	mountOver(mux, e, Authenticator(nobody, nil), refuse, func(*http.Request) string { return "" })
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/admin/settings/bot-check", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "the settings routes are behind the admin API's authentication")
}
