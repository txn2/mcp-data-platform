package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A persona's uploads setting is a switch (#2290): a configuration file that
// still names #2258's recipient lists under it reads them as an unrecognized
// key, which leaves uploads off and is refused under config.strict, and the
// switch loads.
func TestPersonaUploadsIsASwitch(t *testing.T) {
	old := "personas:\n  client-a:\n    roles: [client-a]\n    external: true\n    portal:\n      uploads:\n        share_personas: [marketing]\n"
	cfg, err := LoadConfigFromBytes([]byte(old))
	require.NoError(t, err)
	require.NotNil(t, cfg.Personas.Definitions["client-a"].Portal)
	up := cfg.Personas.Definitions["client-a"].Portal.Uploads
	assert.True(t, up == nil || !*up, "the lists read as nothing: uploads off")
	_, err = LoadConfigFromBytes([]byte("config:\n  strict: true\n" + old))
	require.Error(t, err, "refused under config.strict")

	cfg, err = LoadConfigFromBytes([]byte("personas:\n  client-a:\n    roles: [client-a]\n    external: true\n    portal:\n      uploads: true\n      history: true\n      external_files: none\n"))
	require.NoError(t, err)
	p := cfg.Personas.Definitions["client-a"]
	require.NotNil(t, p.Portal)
	require.NotNil(t, p.Portal.Uploads)
	assert.True(t, *p.Portal.Uploads)
	require.NotNil(t, p.Portal.History)
	assert.True(t, *p.Portal.History)
}
