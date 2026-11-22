package pglint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLint_RolesNamed is #2709's migration rule: a migration names no role,
// since it applies to every environment's schema and the platform grants each
// environment's run role on its own; #2539 allowed the schema's own roles,
// which every environment would share.
func TestLint_RolesNamed(t *testing.T) {
	refused := func(up string) []string {
		findings, _ := Lint(up, Target{Schema: "shop"})
		var out []string
		for _, f := range findings {
			if f.Rule == RuleRoleNamed {
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
		"no role":                                {up: "CREATE TABLE t (id int); REVOKE ALL ON SCHEMA shop FROM PUBLIC; GRANT SELECT ON t TO CURRENT_USER"},
		"the schema's own roles":                 {up: "CREATE ROLE shop_read NOLOGIN; GRANT SELECT ON ALL TABLES IN SCHEMA shop TO shop_read", want: []string{"shop_read", "shop_read"}},
		"another application's role":             {up: `GRANT billing_write TO shop_run`, want: []string{"billing_write", "shop_run"}},
		"a grant to every role":                  {up: `GRANT USAGE ON SCHEMA shop TO PUBLIC`, want: []string{"public"}},
		"altering a role":                        {up: `ALTER ROLE "Billing_run" CONNECTION LIMIT 1`, want: []string{"Billing_run"}},
		"dropping roles":                         {up: `DROP ROLE IF EXISTS shop_old, other_role`, want: []string{"shop_old", "other_role"}},
		"default privileges":                     {up: `ALTER DEFAULT PRIVILEGES IN SCHEMA shop GRANT SELECT ON TABLES TO analytics`, want: []string{"analytics"}},
		"revoking from a role":                   {up: `REVOKE SELECT ON t FROM billing_read`, want: []string{"billing_read"}},
		"membership in another's role at create": {up: `CREATE ROLE shop_x NOLOGIN IN ROLE billing_write`, want: []string{"shop_x", "billing_write"}},
		"a grant inside a DO block":              {up: `DO $body$ BEGIN GRANT billing_write TO shop_run; END $body$`, want: []string{"billing_write", "shop_run"}},
		"a DO block with no role":                {up: `DO $$BEGIN PERFORM 1; END$$`},
		// #2750: SQL a body passes as a string is read unquoted, and SQL it
		// builds at run time naming a role statement is refused.
		"a grant in an EXECUTE literal":   {up: `DO $$ BEGIN EXECUTE 'GRANT billing_write TO shop_run'; END $$`, want: []string{"billing_write", "shop_run"}},
		"a role in a dblink literal":      {up: `DO $$ BEGIN PERFORM dblink_exec('c', 'DROP ROLE old'); END $$`, want: []string{"old"}},
		"a role in a nested literal":      {up: `DO $$ BEGIN EXECUTE 'EXECUTE ''ALTER ROLE nested LOGIN'''; END $$`, want: []string{"nested"}},
		"a grant built at run time":       {up: `DO $$ BEGIN EXECUTE format('GRANT SELECT ON %I TO %I', 't', 'r'); END $$`, want: []string{"SQL built at run time"}},
		"a role built by concatenation":   {up: `DO $$ BEGIN EXECUTE 'CREATE ROLE ' || quote_ident('x'); END $$`, want: []string{"SQL built at run time"}},
		"a table built at run time":       {up: `DO $$ BEGIN EXECUTE format('CREATE TABLE %I (id int)', 't'); END $$`},
		"an EXECUTE literal with no role": {up: `DO $$ BEGIN EXECUTE 'CREATE TABLE t (id int)'; END $$`},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, refused(tc.up))
		})
	}
	findings, _ := Lint(`CREATE ROLE reporting NOLOGIN`, Target{})
	for _, f := range findings {
		assert.NotEqual(t, RuleRoleNamed, f.Rule, "a target naming no schema checks no role")
	}
}

// TestLint_SchemaQualified is #2709's other migration rule: a name qualified
// by the migration's own schema is refused, in statements and bodies alike,
// and a name in another schema or a column of the same name is not.
func TestLint_SchemaQualified(t *testing.T) {
	objects := func(up string) []string {
		findings, _ := Lint(up, Target{Schema: "shop"})
		out := []string{}
		for _, f := range findings {
			if f.Rule == RuleSchemaQualified {
				out = append(out, f.Object)
			}
		}
		return out
	}
	assert.Empty(t, objects("CREATE TABLE orders (shop int); CREATE INDEX ON orders (shop); SELECT pg_catalog.now()"))
	assert.Equal(t, []string{"shop.orders"}, objects("CREATE TABLE shop.orders (id int)"))
	assert.Equal(t, []string{"shop.orders"}, objects(`ALTER TABLE "shop".orders ADD COLUMN note text; ALTER TABLE shop.orders ADD COLUMN x int`)[:1])
	assert.Equal(t, []string{"shop.orders"}, objects("CREATE FUNCTION n() RETURNS bigint LANGUAGE sql AS $$ SELECT count(*) FROM shop.orders $$"))
	findings, _ := Lint("CREATE TABLE shop.orders (id int)", Target{})
	for _, f := range findings {
		assert.NotEqual(t, RuleSchemaQualified, f.Rule, "a target naming no schema checks no name")
	}
}

func TestDollarBody(t *testing.T) {
	assert.Equal(t, "BEGIN END", dollarBody("DO $$BEGIN END$$"))
	assert.Equal(t, " x ", dollarBody("DO $tag$ x $tag$"))
	assert.Empty(t, dollarBody("DO 'no body'"))
	assert.Empty(t, dollarBody("SELECT $1"))
	assert.Equal(t, "y", dollarBody("SELECT $1, $q$y$q$"))
}
