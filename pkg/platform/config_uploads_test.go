package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A persona's uploads setting is a switch (#2290): a configuration file that
// still names #2258's recipient lists under it reads them as uploads on, as
// migration 000219 reads the stored form (#2744), under config.strict too,
// and logs the persona as a deprecated key; the switch loads.
func TestPersonaUploadsIsASwitch(t *testing.T) {
	old := "personas:\n  client-a:\n    roles: [client-a]\n    external: true\n    portal:\n      uploads:\n        share_personas: [marketing]\n" +
		"  client-b:\n    roles: [client-b]\n    external: true\n    portal:\n      resources: shared\n"
	cfg, err := LoadConfigFromBytes([]byte(old))
	require.NoError(t, err)
	require.NotNil(t, cfg.Personas.Definitions["client-a"].Portal)
	up := cfg.Personas.Definitions["client-a"].Portal.Uploads
	require.NotNil(t, up, "the lists read as uploads on")
	assert.True(t, *up)
	assert.Nil(t, cfg.Personas.Definitions["client-b"].Portal.Uploads, "a persona that names no lists is left as it is")
	strict, err := LoadConfigFromBytes([]byte("config:\n  strict: true\n" + old))
	require.NoError(t, err, "the old form is read, not refused, under config.strict")
	assert.True(t, *strict.Personas.Definitions["client-a"].Portal.Uploads)

	// Another key that does not fit is still refused under config.strict.
	_, err = LoadConfigFromBytes([]byte("config:\n  strict: true\npersonas:\n  client-a:\n    roles: [client-a]\n    portal:\n      history:\n        on: true\n"))
	require.Error(t, err)

	cfg, err = LoadConfigFromBytes([]byte("personas:\n  client-a:\n    roles: [client-a]\n    external: true\n    portal:\n      uploads: true\n      history: true\n      external_files: none\n"))
	require.NoError(t, err)
	p := cfg.Personas.Definitions["client-a"]
	require.NotNil(t, p.Portal)
	require.NotNil(t, p.Portal.Uploads)
	assert.True(t, *p.Portal.Uploads)
	require.NotNil(t, p.Portal.History)
	assert.True(t, *p.Portal.History)
}
