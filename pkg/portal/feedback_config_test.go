package portal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFeedbackConfigProjectsTheParentsDependencies pins the seam wiring: the
// feedback surface must receive the same stores and the same authorization
// checker the parent uses, not a second one built from a different set. A
// checker built twice is how a permission comes to mean two things.
func TestFeedbackConfigProjectsTheParentsDependencies(t *testing.T) {
	assets := &mockAssetStore{}
	shares := &mockShareStore{}

	h := NewHandler(Deps{
		AssetStore: assets,
		ShareStore: shares,
		AdminRoles: []string{"admin"},
		PersonaResolver: func(roles []string) *PersonaInfo {
			if len(roles) == 0 {
				return nil
			}
			return &PersonaInfo{Name: "curator", Tools: []string{"apply_knowledge"}}
		},
	}, nil)

	cfg := h.feedbackConfig()
	assert.Same(t, h.access, cfg.Access, "the seam must share the parent's checker")
	assert.Equal(t, assets, cfg.Assets)
	assert.Equal(t, shares, cfg.Shares)

	require.NotNil(t, cfg.PersonaName)
	assert.Equal(t, "curator", cfg.PersonaName([]string{"analyst"}))
	assert.Empty(t, cfg.PersonaName(nil), "roles that resolve to no persona stamp no name")
}

// TestFeedbackConfigWithoutAPersonaResolver leaves PersonaName unset rather
// than installing a closure that would dereference a nil resolver.
func TestFeedbackConfigWithoutAPersonaResolver(t *testing.T) {
	h := NewHandler(Deps{}, nil)
	assert.Nil(t, h.feedbackConfig().PersonaName)
}
