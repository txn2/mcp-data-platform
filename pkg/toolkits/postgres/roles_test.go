package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

func roleCfg() map[string]any {
	return map[string]any{
		cfgKeyHost: "db.internal", cfgKeyDatabase: "shop", cfgKeyUser: "shop_app", cfgKeyPassword: "app-pw",
		CfgKeyPersonaRoles:         map[string]any{"analyst": "shop_analyst", "clerk": "shop_clerk"},
		CfgKeyPersonaRolePasswords: map[string]any{"analyst": "analyst-pw"},
	}
}

func TestParseConfig_PersonaRoles(t *testing.T) {
	c, err := ParseConfig(roleCfg())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"analyst": "shop_analyst", "clerk": "shop_clerk"}, c.PersonaRoles)
	assert.Equal(t, map[string]string{"analyst": "analyst-pw"}, c.PersonaRolePasswords)

	for name, mutate := range map[string]func(map[string]any){
		"empty role":    func(m map[string]any) { m[CfgKeyPersonaRoles] = map[string]any{"analyst": " "} },
		"empty persona": func(m map[string]any) { m[CfgKeyPersonaRoles] = map[string]any{"": "shop_analyst"} },
		"password for an unmapped persona": func(m map[string]any) {
			m[CfgKeyPersonaRolePasswords] = map[string]any{"auditor": "pw"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := roleCfg()
			mutate(m)
			_, err := ParseConfig(m)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "pw", "a refusal never repeats a password")
		})
	}
}

func TestParseFilled_RolePasswordSecret(t *testing.T) {
	prev := secretref.SetConnectionSource(func(_ context.Context, name, connection string) (string, error) {
		if name == "analyst_pw" && connection == "shop" {
			return "from-the-store", nil
		}
		return "", errors.New("secret " + name + " is not allowed for connection " + connection)
	})
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })

	raw := roleCfg()
	raw[CfgKeyPersonaRolePasswords] = map[string]any{"analyst": "{{secret:analyst_pw}}"}
	c, err := parseFilled("shop", raw)
	require.NoError(t, err)
	assert.Equal(t, "from-the-store", c.PersonaRolePasswords["analyst"])
	assert.Equal(t, map[string]any{"analyst": "{{secret:analyst_pw}}"}, raw[CfgKeyPersonaRolePasswords],
		"the stored configuration keeps the reference")

	raw[CfgKeyPersonaRolePasswords] = map[string]any{"analyst": "{{secret:other}}"}
	_, err = parseFilled("shop", raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persona_role_passwords.analyst")

	plain := roleCfg()
	filled, err := fillRolePasswords("shop", plain)
	require.NoError(t, err)
	assert.Equal(t, plain, filled, "nothing to fill returns the configuration as is")
}

func TestIdentity_ContextAndClaims(t *testing.T) {
	assert.Equal(t, Identity{}, IdentityOf(context.Background()), "no identity is every claim empty")
	id := Identity{Persona: "analyst", UserID: "sub-1", Email: "a@example.com", Source: SourceEndpoint}
	assert.Equal(t, id, IdentityOf(WithIdentity(context.Background(), id)))
	assert.Equal(t, [][]byte{[]byte("sub-1"), []byte("a@example.com"), []byte("analyst"), []byte("endpoint")}, id.claims(),
		"claims bind in the order claimsSQL names them")
	assert.Len(t, Identity{}.claims(), 4, "an absent claim is still set, as the empty string")
}

func TestPoolFor(t *testing.T) {
	cfg, err := ParseConfig(roleCfg())
	require.NoError(t, err)
	cfg.ConnectionName = "shop"
	main := &pgxpool.Pool{}
	var built []Config
	c := &conn{cfg: cfg, pool: main, roles: &rolePools{pools: map[string]*pgxpool.Pool{}, build: func(rc Config) (*pgxpool.Pool, error) {
		built = append(built, rc)
		if rc.User == "shop_clerk" {
			return nil, errors.New("no route")
		}
		return &pgxpool.Pool{}, nil
	}}}

	p, err := c.poolFor("")
	require.NoError(t, err)
	assert.Same(t, main, p, "no persona runs as the connection's user")
	p, err = c.poolFor("auditor")
	require.NoError(t, err)
	assert.Same(t, main, p, "a persona with no mapping runs as the connection's user")

	a1, err := c.poolFor("analyst")
	require.NoError(t, err)
	a2, err := c.poolFor("analyst")
	require.NoError(t, err)
	assert.Same(t, a1, a2, "a role's pool is built once")
	assert.NotSame(t, main, a1)
	require.Len(t, built, 1)
	assert.Equal(t, "shop_analyst", built[0].User)
	assert.Equal(t, "analyst-pw", built[0].Password)
	assert.Equal(t, "shop", built[0].Database, "the role logs in to the connection's database")

	_, err = c.poolFor("clerk")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `persona "clerk"'s role on connection "shop"`)

	assert.Equal(t, []*pgxpool.Pool{main, a1}, c.allPools())
	assert.Equal(t, []*pgxpool.Pool{main}, (&conn{pool: main}).allPools(), "a connection mapping no role has one pool")
	assert.Empty(t, (&conn{}).allPools())
}

func TestOnServer_PersonaRoles(t *testing.T) {
	c, err := ParseConfig(roleCfg())
	require.NoError(t, err)
	same := c
	same.User = "tester"
	assert.Equal(t, c.PersonaRoles, onServer(c, same, "clone").PersonaRoles, "the roles exist on the connection's own server")

	other := c
	other.Host = "tests.internal"
	out := onServer(c, other, "clone")
	assert.Nil(t, out.PersonaRoles, "another server has none of the connection's roles")
	assert.Nil(t, out.PersonaRolePasswords)
}

// A connection mapping persona roles builds each role's pool on first use,
// without connecting, reports every pool under the connection's name, and
// closes them all.
func TestRolePools_BuiltReportedAndClosed(t *testing.T) {
	cfg, err := ParseConfig(roleCfg())
	require.NoError(t, err)
	cfg.SSLMode = SSLModeDisable
	tk := NewMulti(MultiConfig{DefaultName: "shop", Instances: map[string]Config{"shop": cfg}})
	c, ok := tk.lookup("shop")
	require.True(t, ok)
	require.NotNil(t, c.roles, "a connection mapping roles has role pools")

	analyst, err := c.poolFor("analyst")
	require.NoError(t, err)
	assert.NotSame(t, c.pool, analyst)
	assert.Equal(t, "shop_analyst", analyst.Config().ConnConfig.User)
	assert.Equal(t, "analyst-pw", analyst.Config().ConnConfig.Password)
	assert.Len(t, c.allPools(), 2)

	samples := tk.poolSamples()
	require.Len(t, samples, 1, "role pools report under the connection's name")
	assert.Equal(t, "shop", samples[0].Connection)
	assert.Equal(t, int64(2*cfg.MaxConns), samples[0].Max, "each role pool is sized max_conns")

	require.NoError(t, tk.Close())
	assert.Empty(t, tk.poolSamples())

	plain := baseConfig()
	plainTK := NewMulti(MultiConfig{DefaultName: "db", Instances: map[string]Config{"db": plain}})
	pc, _ := plainTK.lookup("db")
	assert.Nil(t, pc.roles, "a connection mapping no role builds no role pools")
	_ = plainTK.Close()
}
