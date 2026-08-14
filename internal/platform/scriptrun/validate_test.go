package scriptrun

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findingFor returns the first finding whose message contains want.
func findingFor(t *testing.T, report Report, want string) Finding {
	t.Helper()
	for _, f := range report.Findings {
		if strings.Contains(f.Message, want) {
			return f
		}
	}
	t.Fatalf("no finding mentioning %q in %+v", want, report.Findings)
	return Finding{}
}

func TestValidate_AcceptsAWorkingScript(t *testing.T) {
	report := Validate(`
day = date.add_days(date.of(run.fire_time), -1)
res = platform.query(connection="warehouse", sql="SELECT 1 WHERE d = :d", params={"d": day})
platform.export(name="out", rows=res["rows"], format="csv")
print(json.encode({"n": res["row_count"]}))
`)
	assert.True(t, report.OK)
	assert.Empty(t, report.Findings)
	assert.Equal(t, []string{CapabilityQuery, CapabilityExport}, sortedByCapabilityOrder(report.Capabilities))
	assert.Equal(t, []string{"warehouse"}, report.Connections)
	assert.False(t, report.DynamicConnections)
}

// sortedByCapabilityOrder puts a capability set in the canonical order so the
// assertion above reads as the declared surface rather than as alphabetical
// noise.
func sortedByCapabilityOrder(caps []string) []string {
	out := make([]string, 0, len(caps))
	for _, want := range Capabilities {
		for _, got := range caps {
			if got == want {
				out = append(out, got)
			}
		}
	}
	return out
}

// TestValidate_PythonIsmsGetACorrection is the authoring-ergonomics contract:
// the mistakes an agent's Python instincts produce are answered with what to
// write instead, not with a bare parser message.
func TestValidate_PythonIsmsGetACorrection(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		message  string
		hintWord string
	}{
		{"import", "import os\n", "`import` is not available", "no module system"},
		{"from import", "from os import path\n", "`import` is not available", "no module system"},
		{"try", "try:\n    x = 1\n", "`try`/`except` does not exist", "fail("},
		{"f-string", "x = f\"a {1}\"\n", "f-strings are not supported", ".format("},
		{"clock", "x = datetime.now()\n", "no clock in a script", "run.fire_time"},
		{"random", "x = random.choice([1])\n", "no randomness", "unreproducible"},
		{"filesystem", "x = open('f')\n", "no filesystem or network", "platform.query"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := Validate(tc.source)
			f := findingFor(t, report, tc.message)
			assert.Contains(t, f.Hint, tc.hintWord)
			assert.Positive(t, f.Line)
		})
	}
}

// TestValidate_DialectRestrictionsAreTranslated covers the messages the
// interpreter itself produces, which is where a bare error is least actionable.
func TestValidate_DialectRestrictionsAreTranslated(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		fragment string
		hintWord string
	}{
		{"while", "x = 1\nwhile x:\n    x = 0\n", "while loops", "Iterate over a list"},
		{"undefined", "y = requests_lib\n", "undefined:", "no imports"},
		{"class", "class Foo:\n    pass\n", "got class", "no classes"},
		{"with", "with x as y:\n    pass\n", "got with", "no `with`"},
		{"raise", "raise Exception()\n", "got raise", "fail("},
		{"yield", "def g():\n    yield 1\n", "got yield", "no generators"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := Validate(tc.source)
			assert.False(t, report.OK)
			f := findingFor(t, report, tc.fragment)
			assert.Contains(t, f.Hint, tc.hintWord)
		})
	}
}

// TestValidate_SecretsAreErrors pins the rule that a script carrying an inline
// credential does not get to run while someone decides whether it was serious.
func TestValidate_SecretsAreErrors(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"private key", "k = \"-----BEGIN RSA PRIVATE KEY-----\"\n", "private key"},
		{"aws key", "k = \"AKIAIOSFODNN7EXAMPLE\"\n", "AWS access key id"},
		{"github token", "k = \"ghp_abcdefghijklmnopqrstuvwxyz0123\"\n", "GitHub token"},
		{"slack token", "k = \"xoxb-1234567890-abcdefghij\"\n", "Slack token"},
		{"jwt", "k = \"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U\"\n", "JSON Web Token"},
		{"url credentials", "u = \"postgres://user:secret@db/x\"\n", "URL with embedded credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := Validate(tc.source)
			assert.False(t, report.OK, "a secret must block the script")
			f := findingFor(t, report, tc.want)
			assert.Equal(t, SeverityError, f.Severity)
			assert.Contains(t, f.Hint, "Name a connection")
		})
	}
}

// TestValidate_CredentialShapedAssignmentIsAWarning is the deliberate exception
// to the rule above: this pattern matches a NAMING convention, and a SQL
// predicate comparing a password column is indistinguishable from a credential
// assignment to a text scanner. Blocking would refuse to store a legitimate
// query with no way around it, so it warns and the reviewer decides.
func TestValidate_CredentialShapedAssignmentIsAWarning(t *testing.T) {
	report := Validate("password = \"hunter2000\"\n")
	f := findingFor(t, report, "credential-shaped assignment")
	assert.Equal(t, SeverityWarning, f.Severity)
	assert.True(t, report.OK)

	legitimate := Validate(`platform.query(sql="SELECT id FROM users WHERE password = 'hunter22'")`)
	assert.True(t, legitimate.OK, "a SQL predicate must not be refused as a credential")
}

func TestValidate_UnknownCapabilityIsAnError(t *testing.T) {
	report := Validate(`platform.deliver(name="x")`)
	assert.False(t, report.OK)
	f := findingFor(t, report, "platform.deliver does not exist")
	assert.Contains(t, f.Hint, CapabilityQuery)
	assert.Empty(t, report.Capabilities)
}

// TestValidate_DynamicConnectionIsReported pins the honesty rule: a connection
// list that silently omitted a computed connection would be a false statement
// to a reviewer.
func TestValidate_DynamicConnectionIsReported(t *testing.T) {
	report := Validate(`
conn = "prod" + "-west"
platform.query(connection=conn, sql="SELECT 1")
`)
	require.True(t, report.OK)
	assert.True(t, report.DynamicConnections)
	assert.Empty(t, report.Connections)

	// A non-string literal is equally unknown.
	report = Validate(`platform.query(connection=3, sql="SELECT 1")`)
	assert.True(t, report.DynamicConnections)

	// A call that names no connection at all leaves the list empty without
	// claiming the set is dynamic: it resolves to the default connection.
	report = Validate(`platform.query(sql="SELECT 1")`)
	assert.False(t, report.DynamicConnections)
	assert.Empty(t, report.Connections)
}

func TestValidate_FindingsAreOrderedByLine(t *testing.T) {
	report := Validate("x = 1\n\n\npassword = \"hunter2000\"\n\nimport os\n")
	require.GreaterOrEqual(t, len(report.Findings), 2)
	for i := 1; i < len(report.Findings); i++ {
		assert.LessOrEqual(t, report.Findings[i-1].Line, report.Findings[i].Line)
	}
}

func TestValidate_ParseErrorSkipsInspection(t *testing.T) {
	report := Validate("platform.query(sql=")
	assert.False(t, report.OK)
	assert.NotEmpty(t, report.Findings)
	assert.Empty(t, report.Capabilities, "nothing is claimed about a file that does not parse")
}

// TestValidate_WarningsDoNotBlock separates advice from refusal: an f-string is
// a warning about a shape that will not do what the author meant, while the
// script may still be perfectly runnable.
func TestValidate_WarningsDoNotBlock(t *testing.T) {
	report := Validate("x = \"a\"\ny = x\n# datetime is only mentioned in a comment\n")
	require.NotEmpty(t, report.Findings)
	f := findingFor(t, report, "no clock in a script")
	assert.Equal(t, SeverityWarning, f.Severity)
	assert.True(t, report.OK, "a warning alone must not block the script")
}

func TestIsPredeclaredName(t *testing.T) {
	for _, name := range []string{"platform", "json", "date", "run"} {
		assert.True(t, isPredeclaredName(name), name)
	}
	assert.False(t, isPredeclaredName("time"))
}
