package trino

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateRefusesAPasswordOverPlainHTTP is #2014: the client mcp-trino
// v1.6.0 builds refuses a password over plain HTTP, and Validate says so, for
// every such connection at once, naming each and the remedy.
func TestValidateRefusesAPasswordOverPlainHTTP(t *testing.T) {
	no := false
	mc := MultiConfig{DefaultConnection: "warehouse", Instances: map[string]Config{
		"warehouse": {Host: "trino.internal", Port: 8080, User: "svc", Password: "p", SSL: &no},
		// Leaves the password unset, so it inherits the default's, and its
		// localhost host is plain HTTP.
		"local": {Host: "localhost", User: "svc"},
		"tls":   {Host: "trino.example.com", User: "svc", Password: "p"},
	}}
	err := mc.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnectionRefused))
	msg := err.Error()
	assert.Contains(t, msg, `"warehouse"`)
	assert.Contains(t, msg, `"local"`)
	assert.NotContains(t, msg, `"tls"`, "an https connection with a password is accepted")
	assert.Contains(t, msg, "set ssl: true")

	_, err = NewMulti(mc)
	require.ErrorIs(t, err, ErrConnectionRefused, "the toolkit refuses the same configuration")
}

func TestValidateAcceptsWhatTheClientAccepts(t *testing.T) {
	no, yes := false, true
	for name, mc := range map[string]MultiConfig{
		"no password over http": {Instances: map[string]Config{"a": {Host: "trino", User: "u", SSL: &no}}},
		"password over https":   {Instances: map[string]Config{"a": {Host: "trino", User: "u", Password: "p", SSL: &yes}}},
		"no instances":          {},
	} {
		assert.NoError(t, mc.Validate(), name)
	}
	missing := MultiConfig{DefaultConnection: "gone", Instances: map[string]Config{"a": {Host: "h", User: "u"}}}
	assert.Error(t, missing.Validate())

	noUser := MultiConfig{Instances: map[string]Config{"a": {Host: "h", SSL: &no}}}
	err := noUser.Validate()
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "set ssl: true"), "the TLS remedy is given only for a password over HTTP")
}

// TestAddConnectionRefusesAPasswordOverPlainHTTP holds the same rule for a
// connection added at run time from the database.
func TestAddConnectionRefusesAPasswordOverPlainHTTP(t *testing.T) {
	yes := true
	tk, err := NewMulti(MultiConfig{Instances: map[string]Config{
		"a": {Host: "trino.example.com", User: "u", Password: "p", SSL: &yes},
	}})
	require.NoError(t, err)
	err = tk.AddConnection("insecure", map[string]any{"host": "trino.internal", "port": 8080, "ssl": false})
	require.ErrorIs(t, err, ErrConnectionRefused, "a connection inheriting the default's password over HTTP is refused")
	require.NoError(t, tk.AddConnection("ok", map[string]any{"host": "trino2.example.com", "ssl": true}))
}

// TestValidateConnectionJudgesWithoutAdding is what the admin API asks before
// it stores a connection (#2014): the refusal AddConnection would give, with
// nothing added either way.
func TestValidateConnectionJudgesWithoutAdding(t *testing.T) {
	yes := true
	tk, err := NewMulti(MultiConfig{Instances: map[string]Config{
		"a": {Host: "trino.example.com", User: "u", Password: "p", SSL: &yes},
	}})
	require.NoError(t, err)
	err = tk.ValidateConnection("insecure", map[string]any{"host": "trino.internal", "port": 8080, "ssl": false})
	require.ErrorIs(t, err, ErrConnectionRefused)
	assert.Contains(t, err.Error(), `"insecure"`)
	require.NoError(t, tk.ValidateConnection("ok", map[string]any{"host": "trino2.example.com", "ssl": true}))
	assert.False(t, tk.HasConnection("ok"), "validating adds nothing")

	single := &Toolkit{}
	assert.NoError(t, single.ValidateConnection("x", map[string]any{}), "a single-connection toolkit judges nothing")
}

// TestRefusedNamesEachConnection reports refusals by name, which is how a
// process drops a refused stored connection rather than every one.
func TestRefusedNamesEachConnection(t *testing.T) {
	no, yes := false, true
	mc := MultiConfig{DefaultConnection: "main", Instances: map[string]Config{
		"main": {Host: "trino.example.com", User: "u", Password: "p", SSL: &yes},
		"bad":  {Host: "trino.internal", User: "u", Password: "p", SSL: &no},
	}}
	refused := mc.Refused()
	require.Len(t, refused, 1)
	assert.ErrorIs(t, refused["bad"], ErrConnectionRefused)
	assert.Nil(t, MultiConfig{}.Refused())
	gone := MultiConfig{DefaultConnection: "gone", Instances: map[string]Config{"a": {Host: "h", User: "u"}}}
	assert.Contains(t, gone.Refused(), "gone")
}
