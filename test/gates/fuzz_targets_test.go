package gates

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// fuzzedParsers are the packages reading untrusted input that #2713 holds to
// a fuzz target each: SQL, PostgreSQL text forms, webhook paths, events and
// signatures, archives, map archives, mail, GraphQL, specs, Parquet footers,
// script format strings and patterns, and authenticator seeds.
var fuzzedParsers = []string{
	"internal/pglex", "internal/pgtext", "internal/sqltables", "internal/webhook/jsonpath",
	"internal/webhook/whevent", "internal/webhook/whauth", "internal/hmacsig", "internal/unarchive",
	"internal/pmtiles", "internal/imapmail", "internal/mailtrust", "internal/untrusted",
	"internal/gqlschema", "internal/swagger2", "internal/pathtemplate", "internal/tableparquet",
	"internal/scriptprintf", "internal/scriptre", "internal/totp",
}

var fuzzDecl = regexp.MustCompile(`(?m)^func Fuzz\w+\(f \*testing\.F\)`)

// TestEveryUntrustedParserHasAFuzzTarget fails when a package #2713 names
// loses its last fuzz target, which the daily Fuzzing workflow
// (scripts/fuzz-all.py) would then silently stop fuzzing.
func TestEveryUntrustedParserHasAFuzzTarget(t *testing.T) {
	for _, pkg := range fuzzedParsers {
		files, err := filepath.Glob(filepath.Join("..", "..", pkg, "*_test.go"))
		require(t, err)
		found := false
		for _, f := range files {
			data, err := os.ReadFile(f) // #nosec G304 -- a test file the glob above found
			require(t, err)
			found = found || fuzzDecl.Match(data)
		}
		if !found {
			t.Errorf("%s has no fuzz target; #2713 holds every parser of untrusted input to one", pkg)
		}
	}
}
