package personacfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// TestLegacyUploads (#2744): a persona whose uploads is #2258's mapping is
// found with the line its mapping starts on, the strict decode's refusal of
// that line is accepted and no other, and Apply turns that persona's uploads
// on, creating its portal block, and leaves every other persona alone.
func TestLegacyUploads(t *testing.T) {
	doc := "personas:\n" +
		"  client-a:\n    roles: [a]\n    portal:\n      uploads:\n        share_personas: [marketing]\n" +
		"  client-b:\n    roles: [b]\n    portal:\n      uploads: false\n" +
		"  client-c:\n    roles: [c]\n"
	l := ReadLegacyUploads([]byte(doc))
	assert.Equal(t, LegacyUploads{"client-a": 6}, l)
	assert.True(t, l.Accepts("line 6: cannot unmarshal !!map into bool"))
	assert.False(t, l.Accepts("line 9: cannot unmarshal !!map into bool"))
	assert.False(t, l.Accepts("line 6: field nope not found in type persona.PortalAccess"))

	off := false
	cfg := &PersonasConfig{Definitions: map[string]PersonaDef{
		"client-a": {Roles: []string{"a"}},
		"client-b": {Roles: []string{"b"}, Portal: &persona.PortalAccess{Uploads: &off}},
	}}
	LegacyUploads{"client-a": 6, "gone": 3}.Apply(cfg)
	require.NotNil(t, cfg.Definitions["client-a"].Portal)
	require.NotNil(t, cfg.Definitions["client-a"].Portal.Uploads)
	assert.True(t, *cfg.Definitions["client-a"].Portal.Uploads)
	assert.False(t, *cfg.Definitions["client-b"].Portal.Uploads)
	_, added := cfg.Definitions["gone"]
	assert.False(t, added, "a persona the configuration does not define is not created")
	LegacyUploads(nil).Apply(cfg)

	for _, bad := range []string{"", "- a list\n", "personas: [a]\n", ":\n  - [\n"} {
		assert.Empty(t, ReadLegacyUploads([]byte(bad)), "%q", bad)
	}
	var n yaml.Node
	assert.Nil(t, mappingValue(&n, "personas"), "a node that is not a mapping holds no key")
}
