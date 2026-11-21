package pglex

import (
	"strings"
	"testing"
)

func TestPasswordLiteral(t *testing.T) {
	cases := []struct {
		stmt   string
		object string
	}{
		{"CREATE ROLE app LOGIN PASSWORD 'x'", "CREATE ROLE"},
		{"create user app with encrypted password 'x'", "CREATE USER"},
		{"CREATE GROUP g PASSWORD 'x'", "CREATE GROUP"},
		{"ALTER ROLE app PASSWORD 'x'", "ALTER ROLE"},
		{"ALTER USER app PASSWORD $$x$$", "ALTER USER"},
		{"CREATE USER MAPPING FOR app SERVER s OPTIONS (user 'u', password 'x')", "CREATE USER"},
		{"ALTER ROLE app PASSWORD NULL", ""},
		{"CREATE ROLE app NOLOGIN", ""},
		{"CREATE TABLE users (id int, password text)", ""},
		{"ALTER TABLE users ADD COLUMN password text", ""},
		{"ALTER GROUP g ADD USER app", ""},
		{"-- CREATE ROLE app PASSWORD 'x'\nSELECT 1", ""},
		{"", ""},
	}
	for _, c := range cases {
		object, ok := PasswordLiteral(c.stmt)
		if object != c.object || ok != (c.object != "") {
			t.Errorf("PasswordLiteral(%q) = %q, %v; want %q", c.stmt, object, ok, c.object)
		}
	}
}

func TestRedactPasswords(t *testing.T) {
	const canary = "hunter2-canary"
	cases := []struct{ in, want string }{
		{
			"CREATE TABLE t (id int);\nCREATE ROLE r LOGIN PASSWORD '" + canary + "' VALID UNTIL 'infinity';\nSELECT 'kept'",
			"CREATE TABLE t (id int);\nCREATE ROLE r LOGIN PASSWORD " + RedactedLiteral + " VALID UNTIL " + RedactedLiteral + ";\nSELECT 'kept'",
		},
		{"ALTER ROLE r PASSWORD E'" + canary + "\\'q'", "ALTER ROLE r PASSWORD E" + RedactedLiteral},
		{"ALTER USER r PASSWORD $p$" + canary + "$p$", "ALTER USER r PASSWORD " + RedactedLiteral},
		{"alter role r with password 'it''s " + canary + "'", "alter role r with password " + RedactedLiteral},
		{
			"CREATE USER MAPPING FOR app SERVER s OPTIONS (user 'u', password '" + canary + "')",
			"CREATE USER MAPPING FOR app SERVER s OPTIONS (user 'u', password " + RedactedLiteral + ")",
		},
		{"ALTER ROLE r PASSWORD NULL", "ALTER ROLE r PASSWORD NULL"},
		{"INSERT INTO users (password) VALUES ('kept')", "INSERT INTO users (password) VALUES ('kept')"},
		{"SELECT 'no credential here'", "SELECT 'no credential here'"},
		{"CREATE ROLE r PASSWORD '" + canary, "CREATE ROLE r PASSWORD " + RedactedLiteral},
	}
	for _, c := range cases {
		got := RedactPasswords(c.in)
		if got != c.want {
			t.Errorf("RedactPasswords(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
		if strings.Contains(got, canary) {
			t.Errorf("RedactPasswords(%q) kept the canary", c.in)
		}
	}
}

func TestSplitIsUnchangedByTheRanges(t *testing.T) {
	got := Split("SELECT 1; ; SELECT ';' ;\n")
	if len(got) != 2 || got[0] != "SELECT 1" || got[1] != "SELECT ';'" {
		t.Errorf("Split = %q", got)
	}
}

func TestContainsFold(t *testing.T) {
	for s, want := range map[string]bool{"PassWord": true, "x password y": true, "passwor": false, "": false, "pass word": false} {
		if got := containsFold(s, "password"); got != want {
			t.Errorf("containsFold(%q) = %v, want %v", s, got, want)
		}
	}
}

// TestRedactPasswords_InsideBodies holds #2738: a role given a password inside
// a DO block or a function's body is redacted as one at the top level, and
// the rest of the text is left as written.
func TestRedactPasswords_InsideBodies(t *testing.T) {
	cases := map[string]string{
		`DO $$ BEGIN CREATE ROLE reporting LOGIN PASSWORD 's3cret'; END $$;`:                                               `DO $$ BEGIN CREATE ROLE reporting LOGIN PASSWORD '[REDACTED]'; END $$;`,
		`CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $f$ BEGIN ALTER ROLE x PASSWORD 'p'; END $f$;`:               `CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $f$ BEGIN ALTER ROLE x PASSWORD '[REDACTED]'; END $f$;`,
		"DO $$\nBEGIN\n  IF true THEN\n    CREATE USER u PASSWORD 'p1';\n  END IF;\n  ALTER USER u PASSWORD 'p2';\nEND $$": "DO $$\nBEGIN\n  IF true THEN\n    CREATE USER u PASSWORD '[REDACTED]';\n  END IF;\n  ALTER USER u PASSWORD '[REDACTED]';\nEND $$",
		`DO $o$ BEGIN EXECUTE $i$ SELECT 1 $i$; CREATE ROLE r PASSWORD 'x'; END $o$`:                                       `DO $o$ BEGIN EXECUTE $i$ SELECT 1 $i$; CREATE ROLE r PASSWORD '[REDACTED]'; END $o$`,
		`DO $$ BEGIN RAISE NOTICE 'password reset'; END $$`:                                                                `DO $$ BEGIN RAISE NOTICE 'password reset'; END $$`,
		`DO $$ BEGIN CREATE ROLE r PASSWORD NULL; END $$`:                                                                  `DO $$ BEGIN CREATE ROLE r PASSWORD NULL; END $$`,
	}
	for in, want := range cases {
		if got := RedactPasswords(in); got != want {
			t.Errorf("RedactPasswords(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestPasswordLiteral_InsideBodies(t *testing.T) {
	for stmt, want := range map[string]string{
		`DO $$ BEGIN CREATE ROLE reporting LOGIN PASSWORD 's3cret'; END $$`:                                 "CREATE ROLE",
		`CREATE FUNCTION f() RETURNS void AS $f$ BEGIN ALTER ROLE x PASSWORD 'p'; END $f$ LANGUAGE plpgsql`: "ALTER ROLE",
		`DO $$ BEGIN RAISE NOTICE 'no credential here'; END $$`:                                             "",
	} {
		got, ok := PasswordLiteral(stmt)
		if got != want || ok != (want != "") {
			t.Errorf("PasswordLiteral(%q) = %q, %v; want %q", stmt, got, ok, want)
		}
	}
}
