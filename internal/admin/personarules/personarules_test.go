package personarules

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

func TestNormalize(t *testing.T) {
	t.Run("a recognized action is stored in its canonical case", func(t *testing.T) {
		out := Normalize([]persona.APIRouteRule{
			{Connection: "crm", Action: "ALLOW"},
			{Connection: "crm", Action: "Deny"},
		})
		require.Len(t, out, 2)
		assert.Equal(t, persona.ActionAllow, out[0].Action)
		assert.Equal(t, persona.ActionDeny, out[1].Action)
	})

	t.Run("an unrecognized action is passed through so validation refuses it", func(t *testing.T) {
		// Folding it into an allow here would store as a grant the request
		// that asked for something else.
		out := Normalize([]persona.APIRouteRule{{Connection: "crm", Action: "audit"}})
		require.Len(t, out, 1)
		assert.Equal(t, "audit", out[0].Action)
		assert.Error(t, persona.ValidateAPIRoutes(out))
	})

	got := Normalize([]persona.APIRouteRule{{
		Connection: "  crm-*  ",
		Methods:    []string{" get ", "", "post"},
		Paths:      []string{" /v1/orders/* ", ""},
	}})
	require.Len(t, got, 1)
	assert.Equal(t, "crm-*", got[0].Connection)
	assert.Equal(t, []string{http.MethodGet, http.MethodPost}, got[0].Methods)
	assert.Equal(t, []string{"/v1/orders/*"}, got[0].Paths)
	// An empty action is stored as the allow it has always meant.
	assert.Equal(t, persona.ActionAllow, got[0].Action)

	t.Run("an all-empty list stays nil so it reads as any", func(t *testing.T) {
		out := Normalize([]persona.APIRouteRule{{Connection: "crm", Methods: []string{" ", ""}}})
		require.Len(t, out, 1)
		assert.Nil(t, out[0].Methods)
	})

	t.Run("no rules is no rules", func(t *testing.T) {
		assert.Nil(t, Normalize(nil))
	})
}

func TestValidate(t *testing.T) {
	assert.NoError(t, Validate(&persona.Persona{Name: "a"}))
	assert.Error(t, Validate(&persona.Persona{Name: "a", APIRoutes: []persona.APIRouteRule{{Connection: "crm", Action: "audit"}}}),
		"an action the registry refuses")
	assert.Error(t, Validate(&persona.Persona{Name: "a", Portal: &persona.PortalAccess{Knowledge: "everything"}}),
		"a portal level the registry refuses")
}
