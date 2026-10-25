package resourcesharewire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/resourceshare"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The share routes see the persona a caller resolves to: whether it is
// external and whether it shares.
func TestCallerOf(t *testing.T) {
	off := false
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "staff", Roles: []string{"staff"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "quiet", Roles: []string{"quiet"}, AllowSharing: &off}))
	require.NoError(t, reg.Register(&persona.Persona{
		Name: "vendors", Roles: []string{"vendor"}, External: true,
	}))
	caller := CallerOf(reg)
	assert.Equal(t, "staff", caller([]string{"staff"}).Persona)
	assert.True(t, caller([]string{"staff"}).Sharing)
	assert.False(t, caller([]string{"quiet"}).Sharing)
	v := caller([]string{"vendor"})
	assert.True(t, v.External)
	assert.False(t, v.Sharing)
	assert.Empty(t, caller([]string{"nobody"}).Persona)
}

// Without a database there is nothing to share: the claims pass through and
// nothing is mounted.
func TestNilSharesDoNothing(t *testing.T) {
	var s *Shares
	base := func(*http.Request) (*resource.Claims, error) { return &resource.Claims{Sub: "u"}, nil }
	got, err := s.Bind(base)(nil)
	require.NoError(t, err)
	assert.Nil(t, got.Shared)
	s.Register(http.NewServeMux())
}

type noShares struct{ resourceshare.Shares }

func (noShares) Permission(context.Context, string, resourceshare.Recipient) (string, error) {
	return "editor", nil
}

// Bound claims carry the caller's share lookup, and the share routes
// authenticate with them.
func TestBindCarriesTheLookup(t *testing.T) {
	d := &resourceshare.Deps{Shares: noShares{}, Caller: func([]string) resourceshare.Caller { return resourceshare.Caller{} }}
	s := &Shares{svc: resourceshare.New(*d), caller: d.Caller, deps: d}
	bound := s.Bind(func(*http.Request) (*resource.Claims, error) { return &resource.Claims{Sub: "u"}, nil })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	got, err := bound(req)
	require.NoError(t, err)
	require.NotNil(t, got.Shared)
	assert.Equal(t, "editor", got.Shared("r1"))
	assert.NotNil(t, s.deps.Claims)
	mux := http.NewServeMux()
	s.Register(mux)

	failing := s.Bind(func(*http.Request) (*resource.Claims, error) { return nil, errors.New("signed out") })
	_, err = failing(req)
	assert.Error(t, err)
}
