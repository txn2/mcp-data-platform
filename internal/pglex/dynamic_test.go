package pglex

import (
	"strings"
	"testing"
)

// issue2750Canary is the password every #2750 case gives, which a redacted
// copy must never hold.
const issue2750Canary = "the-2750-value"

// issue2750Forms are the ways a body gives a role a password through SQL it
// passes as a string (#2750), each with the object PasswordLiteral reports.
var issue2750Forms = []struct {
	name, sql, object string
}{
	{"literal", "DO $$\nBEGIN\n  EXECUTE 'ALTER USER somebody WITH PASSWORD ''" + issue2750Canary + "''';\nEND\n$$;", "ALTER USER"},
	{"escape literal", "DO $$ BEGIN EXECUTE E'ALTER ROLE r PASSWORD \\'" + issue2750Canary + "\\''; END $$", "ALTER ROLE"},
	{"literal using", "DO $$ BEGIN EXECUTE 'CREATE ROLE r LOGIN PASSWORD ''" + issue2750Canary + "''' USING 1; END $$", "CREATE ROLE"},
	{"function body", "CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $f$ BEGIN EXECUTE 'ALTER ROLE r PASSWORD ''" + issue2750Canary + "'''; END $f$", "ALTER ROLE"},
	{"dblink_exec literal", "DO $$ BEGIN PERFORM dblink_exec('dbname=x', 'ALTER ROLE r PASSWORD ''" + issue2750Canary + "'''); END $$", "ALTER ROLE"},
	{"assigned then executed", "DO $$ DECLARE s text := 'ALTER ROLE r PASSWORD ''" + issue2750Canary + "'''; BEGIN EXECUTE s; END $$", BuiltAtRunTime},
	{"format", "DO $$ BEGIN EXECUTE format('ALTER ROLE %I PASSWORD %L', 'r', '" + issue2750Canary + "'); END $$", BuiltAtRunTime},
	{"concatenation", "DO $$ BEGIN EXECUTE 'ALTER ROLE r PASSWORD ' || quote_literal('" + issue2750Canary + "'); END $$", BuiltAtRunTime},
	{"variable", "DO $$ DECLARE p text := '" + issue2750Canary + "'; BEGIN EXECUTE format('ALTER ROLE r PASSWORD %L', p); END $$", BuiltAtRunTime},
	{"dblink_exec built", "DO $$ BEGIN PERFORM dblink_exec('dbname=x', format('ALTER USER r PASSWORD %L', '" + issue2750Canary + "')); END $$", BuiltAtRunTime},
	{"top-level dblink built", "SELECT dblink_exec('c', 'ALTER ROLE r PASSWORD ' || quote_literal('" + issue2750Canary + "'))", BuiltAtRunTime},
}

func TestPasswordLiteral_DynamicSQL(t *testing.T) {
	for _, c := range issue2750Forms {
		if object, ok := PasswordLiteral(c.sql); !ok || object != c.object {
			t.Errorf("%s: PasswordLiteral = %q, %v; want %q", c.name, object, ok, c.object)
		}
	}
}

func TestRedactPasswords_DynamicSQL(t *testing.T) {
	for _, c := range issue2750Forms {
		got := RedactPasswords(c.sql)
		if strings.Contains(got, issue2750Canary) {
			t.Errorf("%s: the redacted copy holds the password: %s", c.name, got)
		}
		if again := RedactPasswords(got); again != got {
			t.Errorf("%s: redaction is not idempotent:\n%s\n%s", c.name, got, again)
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%s: nothing was redacted: %s", c.name, got)
		}
	}
	// A literal is written back quoted, so the statement still reads as SQL.
	got := RedactPasswords(issue2750Forms[0].sql)
	if !strings.Contains(got, "EXECUTE 'ALTER USER somebody WITH PASSWORD ''[REDACTED]''';") {
		t.Errorf("the EXECUTE literal was not written back quoted: %s", got)
	}
}

// TestPasswordLiteral_DynamicSQLLeftAlone: SQL built at run time that gives no
// role a password, and a privilege or trigger EXECUTE, are not credentials.
func TestPasswordLiteral_DynamicSQLLeftAlone(t *testing.T) {
	for _, sql := range []string{
		"DO $$ BEGIN EXECUTE 'CREATE TABLE t (password text)'; END $$",
		"DO $$ BEGIN EXECUTE format('UPDATE users SET password = %L', 'x'); END $$",
		"DO $$ BEGIN EXECUTE format('CREATE TABLE %I (id int)', 't'); RAISE NOTICE 'password'; END $$",
		"DO $$ BEGIN GRANT EXECUTE ON FUNCTION f() TO CURRENT_USER; END $$",
		"DO $$ BEGIN EXECUTE 'ALTER ROLE r PASSWORD NULL'; END $$",
		"SELECT 'it''s a password'",
	} {
		if object, ok := PasswordLiteral(sql); ok {
			t.Errorf("PasswordLiteral(%q) = %q", sql, object)
		}
		if got := RedactPasswords(sql); got != sql {
			t.Errorf("RedactPasswords changed %q to %q", sql, got)
		}
	}
}

func TestBuildsSQL(t *testing.T) {
	for sql, want := range map[string]bool{
		"EXECUTE 'SELECT 1'":                    false,
		"EXECUTE E'SELECT 1'":                   false,
		"EXECUTE 'SELECT $1' USING x":           false,
		"EXECUTE s":                             true,
		"EXECUTE format('SELECT %s', 1)":        true,
		"EXECUTE 'SELECT ' || 1":                true,
		"EXECUTE s INTO v":                      true,
		"GRANT EXECUTE ON FUNCTION f() TO r":    false,
		"REVOKE EXECUTE ON FUNCTION f() FROM r": false,
		"CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW EXECUTE FUNCTION f()": false,
		"PERFORM dblink_exec('c', 'SELECT 1')":                                  false,
		"PERFORM dblink_exec('c', s)":                                           true,
		"SELECT dblink('c', format('x %s', 1))":                                 true,
		"SELECT dblink":                                                         false,
		"PERFORM dblink_exec('c', 'SELECT 1'":                                   true,
		"EXECUTE":                                                               false,
	} {
		if got := BuildsSQL(sql); got != want {
			t.Errorf("BuildsSQL(%q) = %v, want %v", sql, got, want)
		}
	}
}

func TestStringLiteralsAndWords(t *testing.T) {
	got := StringLiterals(`SELECT 'a''b', E'c\'d\\n', $$e$$, 'unterminated`)
	want := []string{"a'b", `c'd\\n`, "unterminated"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("StringLiterals = %q, want %q", got, want)
	}
	words := strings.Join(Words(`EXECUTE 'ALTER ROLE ' || r || $q$ PASSWORD $q$`), " ")
	if words != "execute alter role r password" {
		t.Errorf("Words = %q", words)
	}
	if got := requote(unquote(`E'it\'s'`, lexeme{literal: true, start: 1, end: 8})); got != `'it''s'` {
		t.Errorf("requote(unquote) = %q", got)
	}
}
