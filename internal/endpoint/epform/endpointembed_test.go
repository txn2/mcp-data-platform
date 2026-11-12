package epform

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// An endpoint embed marker opens for the endpoint it names, with the asset it
// was written into, until it expires; another key, another endpoint, a tamper,
// a form's marker or an oversized token never open it (#2457).
func TestEndpointEmbed_RoundTripAndRefusals(t *testing.T) {
	key := []byte("embed-key")
	now := time.Unix(1_800_000_000, 0)
	token := SignEndpointEmbed(key, "log-search", "asset-7", now)
	assert.NotEmpty(t, token)

	asset, ok := OpenEndpointEmbed(key, "log-search", token, now.Add(EmbedTTL-time.Second))
	assert.True(t, ok)
	assert.Equal(t, "asset-7", asset)

	for name, open := range map[string]func() (string, bool){
		"expired": func() (string, bool) {
			return OpenEndpointEmbed(key, "log-search", token, now.Add(EmbedTTL+time.Second))
		},
		"another key":  func() (string, bool) { return OpenEndpointEmbed([]byte("other"), "log-search", token, now) },
		"another name": func() (string, bool) { return OpenEndpointEmbed(key, "orders", token, now) },
		"tampered":     func() (string, bool) { return OpenEndpointEmbed(key, "log-search", token+"x", now) },
		"empty":        func() (string, bool) { return OpenEndpointEmbed(key, "log-search", "", now) },
		"too long": func() (string, bool) {
			return OpenEndpointEmbed(key, "log-search", strings.Repeat("a", maxTokenLen+1), now)
		},
		"a form's marker": func() (string, bool) {
			return OpenEndpointEmbed(key, "log-search", SignEmbed(key, "log-search", now), now)
		},
		"no key": func() (string, bool) { return OpenEndpointEmbed(nil, "log-search", token, now) },
	} {
		asset, ok := open()
		assert.False(t, ok, name)
		assert.Empty(t, asset, name)
	}
	assert.False(t, OpenEmbed(key, "log-search", token, now), "an endpoint marker never opens as a form's")
}

func TestEndpointEmbed_NoKeySignsNothing(t *testing.T) {
	assert.Empty(t, SignEndpointEmbed(nil, "log-search", "asset-7", time.Now()))
}
