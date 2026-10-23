package imap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func base() map[string]any {
	return map[string]any{
		"host": "imap.example.com", "username": "platform", "password": "pw",
		"folders": "Platform, Vendors", "allow_senders": []string{"@vendor.example", "ap@other.example"},
		"trusted_authserv_id": "mx.example.com",
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	c, err := ParseConfig(base())
	require.NoError(t, err)
	assert.Equal(t, 993, c.Port)
	assert.Equal(t, "tls", c.TLS)
	assert.True(t, c.RequireAuthPass)
	assert.Equal(t, []string{"Platform", "Vendors"}, c.Folders, "a comma list is read as a list")
	assert.Equal(t, DefaultMaxResults, c.MaxResults)
	assert.Equal(t, DefaultCallTimeout, c.CallTimeout)

	st := base()
	st["tls"] = "starttls"
	c, err = ParseConfig(st)
	require.NoError(t, err)
	assert.Equal(t, 143, c.Port)
}

func TestParseConfig_Refusals(t *testing.T) {
	cases := map[string]func(m map[string]any){
		"host is required":                func(m map[string]any) { delete(m, "host") },
		"in the clear":                    func(m map[string]any) { m["tls"] = "none" },
		"is not a port":                   func(m map[string]any) { m["port"] = 70000 },
		"username and password":           func(m map[string]any) { delete(m, "password") },
		"no all-folders default":          func(m map[string]any) { delete(m, "folders") },
		"allow_senders is required":       func(m map[string]any) { delete(m, "allow_senders") },
		"trusted_authserv_id is required": func(m map[string]any) { delete(m, "trusted_authserv_id") },
		"must be positive":                func(m map[string]any) { m["max_message_bytes"] = 0 },
		"max_results must be":             func(m map[string]any) { m["max_results"] = 501 },
		"neither an address":              func(m map[string]any) { m["allow_senders"] = []any{"vendor.example"} },
		"holds no PEM certificate":        func(m map[string]any) { m["tls_ca_bundle_pem"] = "nope" },
	}
	for want, mutate := range cases {
		m := base()
		mutate(m)
		_, err := ParseConfig(m)
		assert.ErrorContains(t, err, want)
	}
	off := base()
	off["require_auth_pass"] = false
	delete(off, "trusted_authserv_id")
	_, err := ParseConfig(off)
	assert.NoError(t, err, "without required authentication no authserv-id is needed")
}

func TestAccount_FillsNoSecretWhenNoneNamed(t *testing.T) {
	c, err := ParseConfig(base())
	require.NoError(t, err)
	a, err := c.account(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "pw", a.Password)
	assert.Equal(t, "imap.example.com", a.TLSConfig.ServerName)

	c.Password = "{{secret:mailbox}}"
	_, err = c.account(context.Background())
	assert.ErrorContains(t, err, `connection "" password`, "an unfillable reference is an error, never sent as written")
}

func TestParseMultiConfigAndSchema(t *testing.T) {
	m := ParseMultiConfig("a", map[string]map[string]any{"a": base(), "bad": {}})
	assert.Len(t, m.Instances, 1)
	assert.Equal(t, "a", m.Instances["a"].ConnectionName)
	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(ConfigSchemaJSON), &v))
	assert.Len(t, v["properties"], 16)
	assert.Equal(t, 30*time.Second, func() time.Duration {
		b := base()
		b["call_timeout"] = "30s"
		c, _ := ParseConfig(b)
		return c.CallTimeout
	}())
	assert.Nil(t, stringList(42))
}
