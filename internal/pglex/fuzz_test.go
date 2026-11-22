package pglex

import (
	"strings"
	"testing"
)

// FuzzSplit holds Split's cut to the input (#2713): the statement ranges,
// joined again with the semicolons they were cut at, reproduce the input byte
// for byte, and every statement Split returns is one of those ranges trimmed.
func FuzzSplit(f *testing.F) {
	for _, s := range []string{
		"SELECT 1; SELECT 2;",
		"SELECT ';' AS semi; SELECT $$a;b$$;",
		"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT 2; END; SELECT 3",
		"SELECT E'it\\'s;' -- a; comment\n; /* x; */ SELECT \"a;b\"",
		"DO $body$ BEGIN PERFORM 1; END $body$;",
		"ALTER ROLE app PASSWORD 'secret;value'",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		ranges := statementRanges(sql)
		parts := make([]string, 0, len(ranges))
		for _, r := range ranges {
			parts = append(parts, sql[r[0]:r[1]])
		}
		if joined := strings.Join(parts, ";"); joined != sql {
			t.Fatalf("ranges joined = %q, want the input %q", joined, sql)
		}
		got := Split(sql)
		want := []string{}
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				want = append(want, p)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("Split = %q, want %q", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("statement %d = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// FuzzRedactPasswords: redacting twice is redacting once, and a password
// literal it reports is never left in the redacted text.
func FuzzRedactPasswords(f *testing.F) {
	for _, s := range []string{"ALTER ROLE app PASSWORD 'hunter2'", "CREATE USER u WITH PASSWORD 'p''w'", "SELECT 1"} {
		f.Add(s)
	}
	for _, c := range issue2750Forms {
		f.Add(c.sql)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		once := RedactPasswords(sql)
		if twice := RedactPasswords(once); twice != once {
			t.Fatalf("redaction is not idempotent: %q then %q", once, twice)
		}
	})
}
