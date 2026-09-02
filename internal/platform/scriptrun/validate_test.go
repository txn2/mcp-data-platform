package scriptrun

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
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
	assert.Equal(t, []string{script.DestinationPortal}, report.Destinations,
		"an export naming no destination writes to the portal, and the reviewer is shown that")
	assert.False(t, report.DynamicDestinations)
}

// TestValidate_ReportsWhereAScriptWrites is the raw material for the half of
// the capability diff that matters most: a reviewer must be able to read every
// place this code sends data before agreeing to it.
func TestValidate_ReportsWhereAScriptWrites(t *testing.T) {
	report := Validate(`
platform.export(name="a", rows=[])
platform.export(name="b", rows=[], destination="acme-drop", key="2026/sales.csv")
`)
	assert.True(t, report.OK, report.Findings)
	assert.Equal(t, []string{"acme-drop", script.DestinationPortal}, report.Destinations)
	assert.False(t, report.DynamicDestinations)
}

// TestValidate_DynamicDestinationIsReported states the gap rather than hiding
// it: a computed destination cannot be read statically, so the list is known to
// be incomplete and a reviewer must be told rather than shown a false one.
func TestValidate_DynamicDestinationIsReported(t *testing.T) {
	report := Validate(`
where = "acme-" + "drop"
platform.export(name="a", rows=[], destination=where)
`)
	assert.True(t, report.DynamicDestinations)
	assert.Empty(t, report.Destinations,
		"a computed destination is not silently reported as the portal")
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

// TestValidate_UnknownCapabilityIsAnError covers the one thing the member list
// still does: catch a typo. The hint names the helpers and points at the
// generic binding, because a member that does not exist is usually a tool the
// author should be calling by name (#1419).
func TestValidate_UnknownCapabilityIsAnError(t *testing.T) {
	report := Validate(`platform.deliver(name="x")`)
	assert.False(t, report.OK)
	f := findingFor(t, report, "platform.deliver does not exist")
	assert.Contains(t, f.Hint, CapabilityQuery)
	assert.Contains(t, f.Hint, CapabilityCall+"(tool, args)")
	assert.Empty(t, report.Capabilities)
}

// TestValidate_ReportsTheToolsACallNames is the reader's view of the open half
// of the surface. The persona filter decides what a run MAY call; this says
// what this source DOES call, whether the tool was named positionally or by
// keyword.
func TestValidate_ReportsTheToolsACallNames(t *testing.T) {
	report := Validate(`
platform.call("trino_execute", {"connection": "warehouse", "sql": "INSERT INTO t VALUES (1)"})
platform.call(tool="api_invoke_endpoint", args={"connection": "util", "operation_id": "fetch_url"})
platform.call("show_scripts")
`)
	assert.True(t, report.OK, report.Findings)
	assert.Equal(t, []string{CapabilityCall}, report.Capabilities)
	assert.Equal(t, []string{"api_invoke_endpoint", "show_scripts", "trino_execute"}, report.Tools)
	assert.False(t, report.DynamicTools)
	assert.Equal(t, []string{"util", "warehouse"}, report.Connections,
		"a generic call naming a connection is as much a use of it as platform.query naming it")
	assert.False(t, report.DynamicConnections)
}

// TestValidate_DynamicToolIsReported states the gap rather than hiding it. A
// computed tool name, and an argument set this validator cannot read, each make
// a list short — and a list a reader trusts must say when it is.
func TestValidate_DynamicToolIsReported(t *testing.T) {
	report := Validate(`
tool = "trino_" + "execute"
platform.call(tool, {"connection": "warehouse"})
`)
	assert.True(t, report.OK, report.Findings)
	assert.Empty(t, report.Tools)
	assert.True(t, report.DynamicTools)
	assert.Equal(t, []string{"warehouse"}, report.Connections,
		"the connection is still readable even when the tool is not")

	// A computed argument set hides the connection named inside it, and only
	// that: the tool was a literal and the tool list is still complete.
	report = Validate(`
args = {"connection": "warehouse"}
platform.call("trino_execute", args)
`)
	assert.True(t, report.DynamicConnections)
	assert.Empty(t, report.Connections)
	assert.Equal(t, []string{"trino_execute"}, report.Tools)
	assert.False(t, report.DynamicTools,
		"the tool was named literally, so reporting the tool list as short would be a second false statement")

	// A computed connection VALUE inside a readable dict is the same gap the
	// query binding reports for a computed connection= keyword.
	report = Validate(`platform.call("trino_execute", {"connection": "prod" + "-west"})`)
	assert.True(t, report.DynamicConnections)
	assert.Empty(t, report.Connections)
	assert.False(t, report.DynamicTools, "the tool name was a literal")
}

// TestValidate_ACallNamingNoConnectionClaimsNothing keeps the honesty rule
// symmetric: "this call names no connection" is a fact about the source, not a
// gap in the read.
func TestValidate_ACallNamingNoConnectionClaimsNothing(t *testing.T) {
	report := Validate(`
platform.call("show_scripts")
platform.call("search", {"query": "orders"})
`)
	assert.True(t, report.OK, report.Findings)
	assert.Empty(t, report.Connections)
	assert.False(t, report.DynamicConnections)
	assert.False(t, report.DynamicTools)
}

// TestValidate_AnUnreadableArgumentKeyIsNotAConnection covers the dict shapes
// that carry no readable connection. None of them is a gap in the read: the
// call names no connection under a key this validator recognizes, which is the
// same fact as not carrying the key at all.
func TestValidate_AnUnreadableArgumentKeyIsNotAConnection(t *testing.T) {
	for name, source := range map[string]string{
		// A non-string literal key is definitively not "connection", because
		// the keys a tool call takes are strings.
		"a non-string key":       `platform.call("t", {1: "warehouse"})`,
		"another key entirely":   `platform.call("t", {"sql": "SELECT 1"})`,
		"an empty argument dict": `platform.call("t", {})`,
	} {
		t.Run(name, func(t *testing.T) {
			report := Validate(source + "\n")
			assert.Empty(t, report.Connections)
			assert.False(t, report.DynamicConnections)
		})
	}

	// A recognized key whose VALUE is not a readable string IS a gap: the call
	// names a connection this read cannot resolve.
	report := Validate(`platform.call("t", {"connection": 3})` + "\n")
	assert.True(t, report.DynamicConnections)
	assert.Empty(t, report.Connections)

	// So is a COMPUTED key, which might evaluate to "connection". Reading past
	// it would report a complete connection list for a call that names one.
	report = Validate("key = \"conn\"\nplatform.call(\"t\", {key: \"warehouse\"})\n")
	assert.True(t, report.DynamicConnections,
		"a computed key might be the connection, and this read cannot know it is not")
	assert.Empty(t, report.Connections)
}

// TestValidate_ASpreadArgumentMakesACallUnreadable pins the honesty rule for
// f(*args) and f(**kwargs). Every collector reads arguments by position or by
// keyword, and a spread has neither — the values are in a variable — so the
// call contributes nothing and says so, rather than being read past into a
// shorter list presented as a complete one.
func TestValidate_ASpreadArgumentMakesACallUnreadable(t *testing.T) {
	t.Run("an export", func(t *testing.T) {
		report := Validate(`
cfg = {"name": "a", "rows": [], "destination": "acme-drop"}
platform.export(**cfg)
`)
		assert.True(t, report.DynamicDestinations)
		assert.Empty(t, report.Destinations,
			"a spread export must not be reported as a portal write while its dict names a bucket")
	})

	t.Run("a query", func(t *testing.T) {
		report := Validate(`
cfg = {"sql": "SELECT 1", "connection": "warehouse"}
platform.query(**cfg)
`)
		assert.True(t, report.DynamicConnections)
		assert.Empty(t, report.Connections)
	})

	t.Run("a generic call", func(t *testing.T) {
		report := Validate(`
cfg = {"tool": "s3_object", "args": {"connection": "acme-s3"}}
platform.call(**cfg)
`)
		assert.True(t, report.DynamicTools)
		assert.True(t, report.DynamicConnections)
		assert.Empty(t, report.Tools)
		assert.Empty(t, report.Connections)
	})

	t.Run("a refresh", func(t *testing.T) {
		report := Validate(`
cfg = {"name": "dash", "data": {}}
platform.publish_data(**cfg)
`)
		assert.True(t, report.DynamicRefreshTargets)
		assert.Empty(t, report.RefreshTargets)
		assert.Equal(t, []string{script.DestinationPortal}, report.Destinations,
			"a refresh writes to the portal whatever its arguments say")
	})
}

// TestValidate_NoCallMeansAnEmptyToolList keeps the field a list, never null.
func TestValidate_NoCallMeansAnEmptyToolList(t *testing.T) {
	report := Validate(`platform.export(name="a", rows=[])`)
	assert.NotNil(t, report.Tools)
	assert.Empty(t, report.Tools)
	assert.False(t, report.DynamicTools)
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
	assert.Empty(t, report.Tools)
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

// TestValidate_RepeatedDestinationIsNotAPortalDefault covers the misreading a
// set-growth heuristic produces: a second export to a destination already seen
// adds nothing to the set, and treating that as "this one defaulted" would
// report a portal write no line of the script performs — and then refuse the
// approval for not granting it.
func TestValidate_RepeatedDestinationIsNotAPortalDefault(t *testing.T) {
	report := Validate(`
platform.export(name="a", rows=[], destination="acme-drop")
platform.export(name="b", rows=[], destination="acme-drop")
`)
	assert.True(t, report.OK, report.Findings)
	assert.Equal(t, []string{"acme-drop"}, report.Destinations,
		"a delivery-only script writes to no portal, however many exports it has")
}

// TestValidate_RefusesAPositionalDestination is the shape that would make this
// validator state a falsehood: a destination passed by position is invisible to
// a keyword read, and the review surface would positively report a script
// writing to a bucket as writing to the portal.
func TestValidate_RefusesAPositionalDestination(t *testing.T) {
	report := Validate(`platform.export("a", [], "csv", "acme-drop", "2026/x.csv")`)
	assert.False(t, report.OK)
	finding := findingFor(t, report, "at most three positional arguments")
	assert.Equal(t, SeverityError, finding.Severity)
	assert.Contains(t, finding.Hint, "destination=")
	assert.Empty(t, report.Destinations,
		"nothing is claimed about where this script writes, because nothing can be read")
}

// TestValidate_ReportsRefreshTargets pins the reviewer's view of a refresh:
// which asset's data region the script rewrites, whether the name was passed
// positionally or by keyword, plus the capability and the portal destination
// the grant must cover.
func TestValidate_ReportsRefreshTargets(t *testing.T) {
	report := Validate(`
platform.publish_data("dash", {"a": 1})
platform.publish_data(name="kpis", data={"b": 2})
`)
	assert.True(t, report.OK, report.Findings)
	assert.Equal(t, []string{CapabilityPublishData}, report.Capabilities)
	assert.Equal(t, []string{"dash", "kpis"}, report.RefreshTargets)
	assert.False(t, report.DynamicRefreshTargets)
	assert.Equal(t, []string{script.DestinationPortal}, report.Destinations,
		"a refresh writes to the portal, and the reviewer's destination diff must say so")
}

// TestValidate_DynamicRefreshTargetIsReported states the gap rather than
// hiding it: a computed target name cannot be read statically, so the list is
// known to be incomplete.
func TestValidate_DynamicRefreshTargetIsReported(t *testing.T) {
	report := Validate(`
name = "dash-" + run.params["region"]
platform.publish_data(name, {"a": 1})
`)
	assert.True(t, report.OK, report.Findings)
	assert.Empty(t, report.RefreshTargets)
	assert.True(t, report.DynamicRefreshTargets)
	assert.Equal(t, []string{script.DestinationPortal}, report.Destinations)
}

// TestValidate_NoRefreshMeansAnEmptyList keeps the field a list, never null,
// and absent-by-default.
func TestValidate_NoRefreshMeansAnEmptyList(t *testing.T) {
	report := Validate(`platform.export(name="a", rows=[])`)
	assert.NotNil(t, report.RefreshTargets)
	assert.Empty(t, report.RefreshTargets)
	assert.False(t, report.DynamicRefreshTargets)
}
