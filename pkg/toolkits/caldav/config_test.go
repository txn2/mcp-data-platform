package caldav

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfig(t *testing.T) {
	c, err := ParseConfig(map[string]any{"url": "https://cal.example.com/dav/", "auth_mode": "basic", "username": "u", "password": "p"})
	require.NoError(t, err)
	assert.Equal(t, "UTC", c.TimeZone)
	assert.Equal(t, DefaultMaxEvents, c.MaxEvents)
	assert.Equal(t, DefaultMaxRangeDays, c.MaxRangeDays)
	assert.Equal(t, time.UTC, c.Location())

	refusals := map[string]map[string]any{
		"url is required":                {},
		"must be an http or https":       {"url": "ftp://x"},
		"not in the url":                 {"url": "https://u:p@cal.example.com/"},
		"not an IANA zone name":          {"url": "https://x.example.com", "time_zone": "Mars/Base"},
		"max_events must be between":     {"url": "https://x.example.com", "max_events": 0},
		"max_range_days must be between": {"url": "https://x.example.com", "max_range_days": 9999},
		"authorization_code grant":       {"url": "https://x.example.com", "auth_mode": "oauth2_authorization_code"},
	}
	for want, cfg := range refusals {
		_, err := ParseConfig(cfg)
		assert.ErrorContains(t, err, want)
	}
	_, err = ParseConfig(map[string]any{"url": "https://x.example.com", "auth_mode": "bogus"})
	assert.Error(t, err, "the shared upstream policy's refusals pass through")
}

func TestParseMultiConfig_SkipsInvalid(t *testing.T) {
	m := ParseMultiConfig("a", map[string]map[string]any{
		"a":   {"url": "https://a.example.com"},
		"bad": {},
	})
	assert.Len(t, m.Instances, 1)
	assert.Equal(t, "a", m.Instances["a"].ConnectionName)
}

func TestConfigSchemaJSONParses(t *testing.T) {
	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(ConfigSchemaJSON), &v))
	props := asMap(v["properties"])
	for _, k := range []string{"url", "time_zone", "username", "password", "auth_mode"} {
		assert.Contains(t, props, k)
	}
}

func TestLocation_FallsBackToUTC(t *testing.T) {
	assert.Equal(t, time.UTC, Config{TimeZone: "Nowhere/Never"}.Location())
}
