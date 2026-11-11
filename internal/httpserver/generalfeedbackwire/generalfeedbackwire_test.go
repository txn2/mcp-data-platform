package generalfeedbackwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

func newPlatform(t *testing.T, cfg platform.Config) *platform.Platform {
	t.Helper()
	cfg.Server = platform.ServerConfig{Name: "test"}
	cfg.Semantic = platform.SemanticConfig{Provider: "noop"}
	p, err := platform.New(platform.WithConfig(&cfg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// With nothing configured the list is the administrator persona, its people
// read through the member lookup it is given; a configured list seeds it.
func TestService_SeedsFromTheConfiguration(t *testing.T) {
	members := func(_ context.Context, name string) []string {
		if name == "admin" {
			return []string{"ann@b.io"}
		}
		return nil
	}
	people, _, _ := Service(newPlatform(t, platform.Config{}), nil, members).Recipients(context.Background())
	assert.Equal(t, []string{"ann@b.io"}, people, "the default is the administrator persona")

	cfg := platform.Config{}
	cfg.Admin.Persona = "operators"
	cfg.Portal.Feedback.StandaloneRecipients = []string{"requests@b.io", "channel:data-team"}
	people, channels, _ := Service(newPlatform(t, cfg), nil, members).Recipients(context.Background())
	assert.Equal(t, []string{"requests@b.io"}, people)
	assert.Equal(t, []string{"data-team"}, channels)
}

func TestPersonas_SortedByName(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "zeta", DisplayName: "Z"}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "alpha", DisplayName: "A"}))
	got := personas(reg)()
	require.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "Z", got[1].DisplayName)
	assert.Nil(t, personas(nil)())
}

// Mount registers the portal read, and the settings routes only with the
// admin API on.
func TestMount(t *testing.T) {
	routed := func(mux *http.ServeMux, method, path string) bool {
		_, pattern := mux.Handler(httptest.NewRequestWithContext(context.Background(), method, path, http.NoBody))
		return pattern != ""
	}
	pass := func(h http.Handler) http.Handler { return h }
	svc := Service(newPlatform(t, platform.Config{}), nil, nil)

	mux := http.NewServeMux()
	Mount(mux, svc, pass, pass, func(*http.Request) string { return "a@b.io" })
	assert.True(t, routed(mux, http.MethodGet, "/api/v1/portal/feedback/general"))
	assert.True(t, routed(mux, http.MethodPut, "/api/v1/admin/settings/general-feedback"))

	noAdmin := http.NewServeMux()
	Mount(noAdmin, svc, pass, nil, nil)
	assert.True(t, routed(noAdmin, http.MethodGet, "/api/v1/portal/feedback/general"))
	assert.False(t, routed(noAdmin, http.MethodPut, "/api/v1/admin/settings/general-feedback"))

	none := http.NewServeMux()
	Mount(none, nil, pass, pass, nil)
	assert.False(t, routed(none, http.MethodGet, "/api/v1/portal/feedback/general"))
}
