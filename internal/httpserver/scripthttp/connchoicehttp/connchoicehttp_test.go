package connchoicehttp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reachable() []Choice {
	return []Choice{
		{Name: "warehouse", Kind: "trino", Description: "Production warehouse"},
		{Name: "reporting", Kind: "trino", Description: "Reporting cluster"},
		{Name: "lake", Kind: "s3", Description: "Raw object store"},
	}
}

// TestOrEmptyChoices keeps a deployment whose enumeration answers nothing from
// putting a null where a form expects a list.
func TestOrEmptyChoices(t *testing.T) {
	assert.NotNil(t, orEmptyChoices(nil))
	assert.Empty(t, orEmptyChoices(nil))
	assert.Len(t, orEmptyChoices(reachable()), 3)
}

// TestBindableChoices narrows an enumeration to the kind a connection parameter
// can name, which is the kind the query binding reaches (#1384). Offering the
// others offered values the run refuses, and made a name carried by several
// kinds resolve to whichever the enumeration reached first.
func TestBindableChoices(t *testing.T) {
	got := bindableChoices(reachable())
	require.Len(t, got, 2)
	assert.Equal(t, "warehouse", got[0].Name)
	assert.Equal(t, "reporting", got[1].Name)

	assert.Empty(t, bindableChoices(nil))
	assert.Empty(t, bindableChoices([]Choice{{Name: "lake", Kind: "s3"}}))
}
