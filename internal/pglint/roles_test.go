package pglint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLint_RolesOutsideSchema is #2539's migration rule: a migration names
// only its schema's own roles, since a role belongs to the whole server and
// an application's run role is a member of the roles its migrations define.
func TestLint_RolesOutsideSchema(t *testing.T) {
	refused := func(up string) []string {
		findings, _ := Lint(up, Target{Schema: "shop"})
		var out []string
		for _, f := range findings {
			if f.Rule == ruleRoleOutsideSchema {
				assert.Equal(t, SeverityRefused, f.Severity)
				out = append(out, f.Object)
			}
		}
		return out
	}
	for name, tc := range map[string]struct {
		up   string
		want []string
	}{
		"the schema's own roles": {up: `CREATE ROLE shop_read NOLOGIN;
			CREATE ROLE shop_write NOLOGIN IN ROLE shop_read;
			REVOKE ALL ON SCHEMA shop FROM PUBLIC;
			GRANT USAGE ON SCHEMA shop TO shop_read;
			GRANT SELECT ON ALL TABLES IN SCHEMA shop TO shop_read;
			GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA shop TO shop_write;
			ALTER DEFAULT PRIVILEGES IN SCHEMA shop GRANT SELECT ON TABLES TO shop_read;
			GRANT shop_read TO shop_run;
			GRANT SELECT ON shop.t TO CURRENT_USER`},
		"another application's role":             {up: `GRANT billing_write TO shop_run`, want: []string{"billing_write"}},
		"a grant to another's role":              {up: `GRANT SELECT ON shop.orders TO billing_run, shop_read`, want: []string{"billing_run"}},
		"a grant to every role":                  {up: `GRANT USAGE ON SCHEMA shop TO PUBLIC`, want: []string{"public"}},
		"a role not named for the schema":        {up: `CREATE ROLE reporting NOLOGIN`, want: []string{"reporting"}},
		"altering another role":                  {up: `ALTER ROLE "Billing_run" CONNECTION LIMIT 1`, want: []string{"Billing_run"}},
		"dropping roles":                         {up: `DROP ROLE IF EXISTS shop_old, other_role`, want: []string{"other_role"}},
		"default privileges to another":          {up: `ALTER DEFAULT PRIVILEGES IN SCHEMA shop GRANT SELECT ON TABLES TO analytics`, want: []string{"analytics"}},
		"revoking from another's role":           {up: `REVOKE SELECT ON shop.t FROM billing_read`, want: []string{"billing_read"}},
		"membership with admin option":           {up: `GRANT shop_read, ops TO shop_run WITH ADMIN OPTION`, want: []string{"ops"}},
		"membership in another's role at create": {up: `CREATE ROLE shop_x NOLOGIN IN ROLE billing_write`, want: []string{"billing_write"}},
		"an idempotent create of its own role":   {up: `DO $$BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'shop_read') THEN CREATE ROLE shop_read NOLOGIN; END IF; END$$`},
		"a grant inside a DO block":              {up: `DO $body$ BEGIN GRANT billing_write TO shop_run; END $body$`, want: []string{"billing_write"}},
		"a DO block with no role":                {up: `DO $$BEGIN PERFORM 1; END$$`},
		"a group grantee":                        {up: `GRANT SELECT ON shop.t TO GROUP shop_read`},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, refused(tc.up))
		})
	}
	findings, _ := Lint(`CREATE ROLE reporting NOLOGIN`, Target{})
	for _, f := range findings {
		assert.NotEqual(t, ruleRoleOutsideSchema, f.Rule, "a target naming no schema checks no role")
	}
}

func TestDollarBody(t *testing.T) {
	assert.Equal(t, "BEGIN END", dollarBody("DO $$BEGIN END$$"))
	assert.Equal(t, " x ", dollarBody("DO $tag$ x $tag$"))
	assert.Empty(t, dollarBody("DO 'no body'"))
	assert.Empty(t, dollarBody("SELECT $1"))
	assert.Equal(t, "y", dollarBody("SELECT $1, $q$y$q$"))
}
