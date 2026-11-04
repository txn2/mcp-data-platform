package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

var (
	target17 = Target{Major: 17}
	target18 = Target{Major: 18}
)

func TestLintSecurity_EachRuleAndItsSafeForm(t *testing.T) {
	cases := []struct {
		name, up, safe, rule string
		target               Target
	}{
		{
			name:   "definer without search_path",
			up:     "CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER AS $$SELECT 1$$; REVOKE ALL ON FUNCTION f() FROM PUBLIC",
			safe:   "CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path = app, pg_temp AS $$SELECT 1$$; REVOKE ALL ON FUNCTION f() FROM PUBLIC",
			rule:   ruleDefinerSearchPath,
			target: target17,
		},
		{
			name:   "definer with pg_temp first",
			up:     "CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path TO pg_temp, app AS $$SELECT 1$$; REVOKE EXECUTE ON FUNCTION f FROM PUBLIC",
			safe:   "CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path TO 'app, pg_temp' AS $$SELECT 1$$; REVOKE EXECUTE ON FUNCTION f FROM PUBLIC",
			rule:   ruleDefinerSearchPath,
			target: target17,
		},
		{
			name:   "definer from current",
			up:     "CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path FROM CURRENT AS $$SELECT 1$$; REVOKE ALL ON ALL FUNCTIONS IN SCHEMA app FROM PUBLIC",
			safe:   "CREATE FUNCTION f() RETURNS int LANGUAGE sql SECURITY INVOKER AS $$SELECT 1$$",
			rule:   ruleDefinerSearchPath,
			target: target17,
		},
		{
			name:   "definer left to PUBLIC",
			up:     "CREATE OR REPLACE FUNCTION app.f(a int) RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path = app, pg_temp AS $$SELECT a$$",
			safe:   "CREATE OR REPLACE FUNCTION app.f(a int) RETURNS int LANGUAGE sql SECURITY DEFINER SET search_path = app, pg_temp AS $$SELECT a$$; REVOKE ALL ON FUNCTION app.f(int), g() FROM PUBLIC",
			rule:   ruleDefinerPublic,
			target: target17,
		},
		{
			name:   "default privileges after the function do not cover it",
			up:     "CREATE PROCEDURE p() LANGUAGE sql SECURITY DEFINER SET search_path = pg_temp AS $$SELECT 1$$; ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON ROUTINES FROM PUBLIC",
			safe:   "ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON ROUTINES FROM PUBLIC; CREATE PROCEDURE p() LANGUAGE sql SECURITY DEFINER SET search_path = pg_temp AS $$SELECT 1$$",
			rule:   ruleDefinerPublic,
			target: target17,
		},
		{
			name: "row security not forced",
			up:   "ALTER TABLE notes ENABLE ROW LEVEL SECURITY",
			safe: "ALTER TABLE notes ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY",
			rule: ruleRLSNotForced,
		},
		{
			name: "view over a table this migration secures",
			up:   "ALTER TABLE notes ENABLE ROW LEVEL SECURITY; ALTER TABLE notes FORCE ROW LEVEL SECURITY; CREATE VIEW mine AS SELECT n.id FROM other o JOIN notes AS n ON n.o = o.id",
			safe: "ALTER TABLE notes ENABLE ROW LEVEL SECURITY; ALTER TABLE notes FORCE ROW LEVEL SECURITY; CREATE VIEW mine WITH (security_invoker = true) AS SELECT id FROM notes",
			rule: ruleViewOverRLS,
		},
		{
			name:   "view over a table the server already secures",
			up:     "CREATE OR REPLACE VIEW mine AS SELECT id FROM app.other, app.notes n",
			safe:   "CREATE VIEW mine AS SELECT id FROM app.notes; ALTER VIEW mine SET (security_invoker)",
			rule:   ruleViewOverRLS,
			target: Target{Major: 17, RowSecurity: map[string]bool{"notes": true}},
		},
		{
			name:   "index on a virtual column this migration declares",
			up:     "CREATE TABLE t (a int, b int GENERATED ALWAYS AS (a * 2)); CREATE INDEX t_b ON t (b)",
			safe:   "CREATE TABLE t (a int, b int GENERATED ALWAYS AS (a * 2) STORED); CREATE INDEX t_b ON t (b)",
			rule:   ruleIndexVirtualColumn,
			target: target18,
		},
		{
			name:   "index on a virtual column the server holds",
			up:     "CREATE INDEX CONCURRENTLY t_b ON t USING btree (lower(b))",
			safe:   "CREATE INDEX CONCURRENTLY t_a ON t USING btree (a)",
			rule:   ruleIndexVirtualColumn,
			target: Target{Major: 18, Virtual: map[string]map[string]bool{"t": {"b": true}}},
		},
		{
			name:   "virtual column added on 18 then indexed",
			up:     "ALTER TABLE t ADD COLUMN c int GENERATED ALWAYS AS (a + 1) VIRTUAL; CREATE INDEX t_c ON t (c)",
			safe:   "ALTER TABLE t ADD COLUMN c int GENERATED ALWAYS AS (a + 1) STORED; CREATE INDEX t_c ON t (c)",
			rule:   ruleIndexVirtualColumn,
			target: target18,
		},
		{name: "uuidv7 on 17", up: "CREATE TABLE t (id uuid DEFAULT uuidv7())", safe: "CREATE TABLE t (id uuid DEFAULT gen_random_uuid())", rule: ruleNeedsNewerServer, target: target17},
		{name: "returning old on 17", up: "UPDATE t SET a = 1 WHERE id = 2 RETURNING old.a, new.a", safe: "UPDATE t SET a = 1 WHERE id = 2 RETURNING a", rule: ruleNeedsNewerServer, target: target17},
		{name: "returning with on 17", up: "DELETE FROM t WHERE id = 2 RETURNING WITH (OLD AS o) o.a", safe: "DELETE FROM t WHERE id = 2 RETURNING a", rule: ruleNeedsNewerServer, target: target17},
		{name: "virtual column on 17", up: "CREATE TABLE t (a int, b int GENERATED ALWAYS AS (a * 2))", safe: "CREATE TABLE t (a int, b int GENERATED ALWAYS AS (a * 2) STORED, c int GENERATED ALWAYS AS IDENTITY)", rule: ruleNeedsNewerServer, target: target17},
		{name: "without overlaps on 17", up: "CREATE TABLE r (id int, during tstzrange, PRIMARY KEY (id, during WITHOUT OVERLAPS))", safe: "CREATE TABLE r (id int, during tstzrange, EXCLUDE USING gist (id WITH =, during WITH &&))", rule: ruleNeedsNewerServer, target: target17},
		{name: "not enforced on 17", up: "ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0) NOT ENFORCED", safe: "ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0) NOT VALID", rule: ruleNeedsNewerServer, target: target17},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := Lint(tc.up, tc.target)
			if !slices.Contains(rulesOf(got), tc.rule) {
				t.Errorf("Lint(%q) = %v, want %s", tc.up, rulesOf(got), tc.rule)
			}
			safe, _ := Lint(tc.safe, tc.target)
			if slices.Contains(rulesOf(safe), tc.rule) {
				t.Errorf("Lint(%q) = %v, the safe form still reports %s", tc.safe, rulesOf(safe), tc.rule)
			}
		})
	}
}

func TestLintSecurity_VersionRulesAreSilentOn18AndWhenUnread(t *testing.T) {
	up := "CREATE TABLE t (id uuid DEFAULT uuidv7(), a int, b int GENERATED ALWAYS AS (a * 2), CHECK (a > 0) NOT ENFORCED); UPDATE t SET a = 1 WHERE a = 0 RETURNING old.a"
	for _, target := range []Target{target18, {}} {
		got, _ := Lint(up, target)
		assert.NotContains(t, rulesOf(got), ruleNeedsNewerServer, "major %d", target.Major)
	}
	got, _ := Lint(up, target17)
	assert.Len(t, slices.DeleteFunc(rulesOf(got), func(r string) bool { return r != ruleNeedsNewerServer }), 4,
		"uuidv7, the virtual column, NOT ENFORCED and RETURNING old")
	got, _ = Lint("CREATE TABLE t (a int, b int GENERATED ALWAYS AS (a * 2)); CREATE INDEX t_b ON t (b)", target17)
	assert.NotContains(t, rulesOf(got), ruleIndexVirtualColumn, "17 has no virtual column to index")
}

func TestLintSecurity_Refusals(t *testing.T) {
	for up, rule := range map[string]string{
		"COPY t FROM PROGRAM 'curl http://x'":                                    ruleCopyProgram,
		"COPY (SELECT 1) TO PROGRAM 'sh'":                                        ruleCopyProgram,
		"CREATE TRUSTED PROCEDURAL LANGUAGE plsample":                            ruleUntrustedCode,
		"CREATE OR REPLACE LANGUAGE plpython3u":                                  ruleUntrustedCode,
		"CREATE FUNCTION f() RETURNS int LANGUAGE plpython3u AS $$return 1$$":    ruleUntrustedCode,
		"CREATE FUNCTION f() RETURNS int AS 'lib', 'f' LANGUAGE 'C'":             ruleUntrustedCode,
		"CREATE PROCEDURE p() LANGUAGE plperlu AS $$1$$":                         ruleUntrustedCode,
		"CREATE EXTENSION IF NOT EXISTS plperlu":                                 ruleUntrustedCode,
		"DO LANGUAGE pltclu $$puts 1$$":                                          ruleUntrustedCode,
		"CREATE EVENT TRIGGER audit ON ddl_command_end EXECUTE FUNCTION audit()": ruleEventTrigger,
		"ALTER SYSTEM SET work_mem = '1GB'":                                      ruleAlterSystem,
	} {
		got, _ := Lint(up, target17)
		r := Refused(got)
		if len(r) != 1 || r[0].Rule != rule || r[0].Statement != 1 {
			t.Errorf("Lint(%q) refused %v, want %s", up, r, rule)
		}
	}
	for _, up := range []string{
		"COPY t FROM STDIN", "CREATE FUNCTION f() RETURNS int LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$",
		"DO $$BEGIN NULL; END$$", "CREATE EXTENSION pgcrypto", "ALTER TABLE t SET (fillfactor = 70)",
	} {
		got, _ := Lint(up, target17)
		assert.Empty(t, Refused(got), up)
	}
}

func TestReviewRefusesAndReportsTheTarget(t *testing.T) {
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "dev@example.com", IsAdmin: true})
	tk, store := schemaToolkit(&fakeLedger{exists: true}, baseConfig())
	_, refusal := run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "sys", Up: "CREATE TABLE a (id int);\nALTER SYSTEM SET work_mem = '1GB'"})
	assert.Contains(t, refusal, "migration 2 is refused")
	assert.Contains(t, refusal, "statement 2 (ALTER SYSTEM)")
	assert.Empty(t, store.migrations, "nothing saved")

	tk, _ = schemaToolkit(&fakeLedger{exists: true, targetErr: errors.New("connection refused")}, baseConfig())
	out, refusal := run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "t", Up: "CREATE TABLE t (id uuid DEFAULT uuidv7())"})
	require.Empty(t, refusal)
	findings, _ := out["findings"].([]any)
	require.Len(t, findings, 1, "the version rules did not run, and the add says so")
	first, _ := findings[0].(map[string]any)
	assert.Equal(t, ruleTargetUnread, first["rule"])

	tk, _ = schemaToolkit(&fakeLedger{exists: true, target: Target{Major: 18}}, baseConfig())
	out, refusal = run(ctx, t, tk, schemaInput{Command: schemaAdd, Version: 2, Name: "t", Up: "CREATE TABLE t (id uuid DEFAULT uuidv7())"})
	require.Empty(t, refusal)
	assert.Empty(t, out["findings"], "18 has uuidv7")
}

func TestWithServerFindings(t *testing.T) {
	plain := errors.New("plain")
	assert.Equal(t, plain, withServerFindings(plain, nil))
	failed := &RefusedError{Code: CodeFromZeroFailed, msg: "applied from zero: syntax error"}
	assert.Equal(t, failed, withServerFindings(failed, []Finding{{Rule: ruleRLSNotForced}}))
	got := withServerFindings(failed, []Finding{{Rule: ruleNeedsNewerServer, Statement: 1, Effect: "uses uuidv7()", Safe: "gen_random_uuid()"}})
	var refused *RefusedError
	require.ErrorAs(t, got, &refused)
	assert.Equal(t, CodeFromZeroFailed, refused.Code)
	assert.Contains(t, got.Error(), "syntax error. The review found: statement 1 uses uuidv7() (instead: gen_random_uuid())")
}

func TestReviewReadsTheSchemasRowSecurityAndVirtualColumns(t *testing.T) {
	ctx := mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u1", Email: "dev@example.com", IsAdmin: true})
	tk, _ := schemaToolkit(&fakeLedger{exists: true, target: Target{
		Major: 18, RowSecurity: map[string]bool{"notes": true}, Virtual: map[string]map[string]bool{"t": {"b": true}},
	}}, baseConfig())
	out, refusal := run(ctx, t, tk, schemaInput{
		Command: schemaAdd, Version: 2, Name: "v",
		Up: "CREATE VIEW mine AS SELECT id FROM notes;\nCREATE INDEX t_b ON t (b)",
	})
	require.Empty(t, refusal)
	findings, _ := out["findings"].([]any)
	rules := make([]string, 0, len(findings))
	for _, raw := range findings {
		f, _ := raw.(map[string]any)
		rule, _ := f["rule"].(string)
		rules = append(rules, rule)
	}
	assert.ElementsMatch(t, []string{ruleViewOverRLS, ruleIndexVirtualColumn, RuleIndexLocks}, rules)

	target, unread := tk.lintTarget(ctx, "nope", "public")
	assert.Zero(t, target.Major)
	assert.Nil(t, unread, "an unknown connection is refused by the add, not reported here")
}

func TestLintSecurity_PasswordLiteral(t *testing.T) {
	const secret = "hunter2-canary-2411"
	refused := []string{
		"CREATE ROLE app LOGIN PASSWORD '" + secret + "'",
		"CREATE USER app WITH PASSWORD '" + secret + "'",
		"ALTER ROLE app PASSWORD '" + secret + "'",
		"ALTER USER app WITH ENCRYPTED PASSWORD '" + secret + "'",
		"CREATE USER MAPPING FOR app SERVER remote OPTIONS (user 'x', password '" + secret + "')",
	}
	for _, up := range refused {
		findings, _ := Lint(up, target17)
		i := slices.IndexFunc(findings, func(f Finding) bool { return f.Rule == rulePasswordLiteral })
		require.GreaterOrEqual(t, i, 0, up)
		assert.Equal(t, SeverityRefused, findings[i].Severity, up)
		for _, f := range findings {
			assert.NotContains(t, f.Object+f.Effect+f.Safe, secret, "a finding never repeats the value")
		}
	}
	for _, up := range []string{
		"CREATE ROLE app NOLOGIN",
		"ALTER ROLE app PASSWORD NULL",
		"CREATE TABLE users (id int, password text)",
		"ALTER TABLE users ADD COLUMN password text",
	} {
		findings, _ := Lint(up, target17)
		assert.False(t, slices.ContainsFunc(findings, func(f Finding) bool { return f.Rule == rulePasswordLiteral }), up)
	}
}
