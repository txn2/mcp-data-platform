package platform

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogsemantic"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// TestCreateSemanticProviderIsTheCatalog proves the platform catalog is the
// semantic layer wherever there is a database (#1919), and that noop and a
// missing database still give the noop provider.
func TestCreateSemanticProviderIsTheCatalog(t *testing.T) {
	for _, provider := range []string{""} {
		p := &Platform{config: &Config{Semantic: SemanticConfig{Provider: provider}}, db: &sql.DB{}}
		got, err := p.createSemanticProvider()
		require.NoError(t, err)
		assert.Equal(t, catalogsemantic.Name, got.Name(), provider)
		assert.True(t, nativeCatalog(got))
	}

	p := &Platform{config: &Config{Semantic: SemanticConfig{Provider: "noop"}}, db: &sql.DB{}}
	got, err := p.createSemanticProvider()
	require.NoError(t, err)
	assert.IsType(t, &semantic.NoopProvider{}, got)
	assert.False(t, nativeCatalog(got))

	got, err = (&Platform{config: &Config{}}).createSemanticProvider()
	require.NoError(t, err)
	assert.IsType(t, &semantic.NoopProvider{}, got, "no database, no catalog")

	_, err = (&Platform{config: &Config{Semantic: SemanticConfig{Provider: "atlas"}}}).createSemanticProvider()
	assert.Error(t, err)
	assert.False(t, nativeCatalog(nil))
}
