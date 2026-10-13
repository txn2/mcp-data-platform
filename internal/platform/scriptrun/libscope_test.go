package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scopedExporter lands a library output in the library the request names, as
// the platform's lander does, so the record's scope can be read off the run.
type scopedExporter struct {
	landingExporter
}

func (e *scopedExporter) Export(ctx context.Context, req ExportRequest) (*ExportResult, error) {
	res, err := e.landingExporter.Export(ctx, req)
	if err != nil {
		return nil, err
	}
	res.Scope, res.ScopeID = req.Library.Scope, req.Library.ScopeID
	if res.Scope == "" {
		res.Scope, res.ScopeID = "user", "jane@example.com"
	}
	return res, nil
}

func newScopedExporter() *scopedExporter {
	return &scopedExporter{landingExporter: *newLandingExporter()}
}

// scope and scope_id name the library a "resources" output is filed in
// (#2080): the request carries it, and the record the script gets back says
// where the file landed.
func TestExport_ScopeNamesTheLibrary(t *testing.T) {
	cases := map[string]struct {
		args          string
		want          ResourceLibrary
		scope, scopID string
	}{
		"no scope":         {``, ResourceLibrary{}, "user", "jane@example.com"},
		"user, own":        {`, scope="user"`, ResourceLibrary{}, "user", "jane@example.com"},
		"global":           {`, scope="global"`, ResourceLibrary{Scope: "global"}, "global", ""},
		"persona":          {`, scope="persona", scope_id="finance"`, ResourceLibrary{Scope: "persona", ScopeID: "finance"}, "persona", "finance"},
		"another's":        {`, scope="user", scope_id="bob@example.com"`, ResourceLibrary{Scope: "user", ScopeID: "bob@example.com"}, "user", "bob@example.com"},
		"padded whitespce": {`, scope=" global "`, ResourceLibrary{Scope: "global"}, "global", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exporter := newScopedExporter()
			result, err := destinationRun(t, `
out = platform.export(name="orders", rows=[{"a": 1}], format="csv", destination="resources", key="datasets/orders.csv"`+tc.args+`)
print(out["scope"], out.get("scope_id", "-"))
`, nil, exporter)
			require.NoError(t, err)
			require.Len(t, exporter.requests, 1)
			assert.Equal(t, tc.want, exporter.requests[0].Library)
			assert.Equal(t, tc.scope, result.Exports[0].Scope)
			assert.Equal(t, tc.scopID, result.Exports[0].ScopeID)
			want := tc.scopID
			if want == "" {
				want = "-"
			}
			assert.Contains(t, result.Log, tc.scope+" "+want)
		})
	}
}

// A scope that names no library, or one given where no library is written, is
// refused at the call, before anything is written.
func TestExport_AScopeThatNamesNoLibraryIsRefused(t *testing.T) {
	cases := map[string]struct {
		call string
		want string
	}{
		"to the portal":      {`platform.export(name="o", rows=[{"a": 1}], scope="global")`, `only the "resources" destination`},
		"id with no scope":   {`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope_id="finance")`, `scope_id names a library only together`},
		"persona with no id": {`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope="persona")`, `needs scope_id`},
		"global with an id":  {`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope="global", scope_id="x")`, `takes no scope_id`},
		"an unknown scope":   {`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope="team")`, `is not one of "user", "persona" or "global"`},
		"scope not a string": {`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope=1)`, `scope`},
		"passed by position": {`platform.export("o", [{"a": 1}], "csv", "resources", "d/o.csv")`, `pass destination and key by name`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exporter := newScopedExporter()
			_, err := destinationRun(t, tc.call, nil, exporter)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, exporter.requests)
		})
	}
}

// A draft previews the library it would write to.
func TestExport_ADraftPreviewsTheLibrary(t *testing.T) {
	result, err := destinationRun(t,
		`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope="persona", scope_id="finance")`,
		nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview)
	assert.Equal(t, "persona", result.Exports[0].Scope)
	assert.Equal(t, "finance", result.Exports[0].ScopeID)

	result, err = destinationRun(t, `platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv")`, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "user", result.Exports[0].Scope, "the run's own library")
	assert.Empty(t, result.Exports[0].ScopeID)

	result, err = destinationRun(t, `platform.export(name="o", rows=[{"a": 1}])`, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, result.Exports[0].Scope, "a portal output is in no library")
}

// The first page of an appended output fixes its library; a later page naming
// another is refused, and the pages that agree are written as one file there.
func TestExport_AnAppendedOutputKeepsItsLibrary(t *testing.T) {
	exporter := newScopedExporter()
	_, err := destinationRun(t, `
platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope="global", append=True)
platform.export(name="o", rows=[{"a": 2}], destination="resources", key="d/o.csv", scope="global", append=True)
`, nil, exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	assert.Equal(t, ResourceLibrary{Scope: "global"}, exporter.requests[0].Library)

	exporter = newScopedExporter()
	_, err = destinationRun(t, `
platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", scope="global", append=True)
platform.export(name="o", rows=[{"a": 2}], destination="resources", key="d/o.csv", append=True)
`, nil, exporter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output "o" was started in the global library`)
	assert.Empty(t, exporter.requests, "a run that fails part-way writes none of it")
}

// validate reads the library off the source and reports it in the reach; a
// scope it cannot read, or one that names no library, is an error, the same
// as at run time.
func TestValidate_ReadsTheLibrary(t *testing.T) {
	report := Validate(`
SHARED = "finance"

def main():
    platform.export(name="a", rows=[], destination="resources", key="d/a.csv")
    platform.export(name="b", rows=[], destination="resources", key="d/b.csv", scope="user")
    platform.export(name="c", rows=[], destination="resources", key="d/c.csv", scope="global")
    platform.export(name="d", rows=[], destination="resources", key="d/d.csv", scope="persona", scope_id=SHARED)
    platform.export(name="e", rows=[])
`)
	assert.True(t, report.OK, "%+v", report.Findings)
	assert.Equal(t, []ResourceLibrary{{}, {Scope: "global"}, {Scope: "persona", ScopeID: "finance"}}, report.ResourceLibraries)

	cases := map[string]string{
		"a computed scope":    `platform.export(name="a", rows=[], destination="resources", key="d/a.csv", scope=pick())`,
		"a computed scope_id": `platform.export(name="a", rows=[], destination="resources", key="d/a.csv", scope="persona", scope_id=who)`,
		"scope on the portal": `platform.export(name="a", rows=[], scope="global")`,
		"persona with no id":  `platform.export(name="a", rows=[], destination="resources", key="d/a.csv", scope="persona")`,
		"an unknown scope":    `platform.export(name="a", rows=[], destination="resources", key="d/a.csv", scope="team")`,
		"scope on a bucket":   `platform.export(name="a", rows=[], destination="acme-drop", key="a.csv", scope="global")`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			report := Validate(source)
			assert.False(t, report.OK, "%+v", report.Findings)
			assert.Empty(t, report.ResourceLibraries)
		})
	}

	// A computed destination is reported as one, and the library with it.
	report = Validate(`platform.export(name="a", rows=[], destination=where, key="d/a.csv", scope="global")`)
	assert.True(t, report.DynamicDestinations)
	assert.Empty(t, report.ResourceLibraries)
	assert.Equal(t, []ResourceLibrary{}, Validate(`x = 1`).ResourceLibraries, "never null")
}

// A library reads the way a refusal names it.
func TestResourceLibraryLabel(t *testing.T) {
	assert.Equal(t, "the library of the person the run acts for", ResourceLibrary{}.Label())
	assert.Equal(t, "the library of the person the run acts for", ResourceLibrary{Scope: "user"}.Label())
	assert.Equal(t, "the global library", ResourceLibrary{Scope: "global"}.Label())
	assert.Equal(t, "the finance persona library", ResourceLibrary{Scope: "persona", ScopeID: "finance"}.Label())
	assert.Equal(t, "the library of bob@example.com", ResourceLibrary{Scope: "user", ScopeID: "bob@example.com"}.Label())
}

// An appended output is recorded where the script started it, so the run's
// outputs read in the order the script made them, as testing.outputs()
// lists them, whichever comes first.
func TestExport_AnAppendedOutputIsRecordedWhereItWasStarted(t *testing.T) {
	cases := map[string]struct {
		source string
		want   []string
	}{
		"appended first": {`
platform.export(name="paged", rows=[{"a": 1}], destination="resources", key="d/p.csv", append=True)
platform.export(name="whole", rows=[{"a": 1}])
platform.export(name="paged", rows=[{"a": 2}], destination="resources", key="d/p.csv", append=True)
platform.export(name="last", rows=[{"a": 1}])
`, []string{"paged", "whole", "last"}},
		"appended between": {`
platform.export(name="whole", rows=[{"a": 1}])
platform.export(name="paged", rows=[{"a": 1}], destination="resources", key="d/p.csv", append=True)
platform.export(name="other", rows=[{"a": 1}], destination="resources", key="d/o.csv", append=True)
platform.export(name="last", rows=[{"a": 1}])
`, []string{"whole", "paged", "other", "last"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := destinationRun(t, tc.source, nil, newScopedExporter())
			require.NoError(t, err)
			got := make([]string, 0, len(result.Exports))
			for _, e := range result.Exports {
				got = append(got, e.Name)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// index= decides how search indexes a "resources" output (#2111): the request
// carries it to the lander, and it is refused where no library is written.
func TestExport_IndexReachesTheLander(t *testing.T) {
	exporter := newScopedExporter()
	_, err := destinationRun(t,
		`platform.export(name="day", rows=[{"a": 1}], destination="resources", key="pipelines/nltv/2019-01-01.csv", index=" None ")`,
		nil, exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	assert.Equal(t, "none", exporter.requests[0].Index)

	exporter = newScopedExporter()
	_, err = destinationRun(t, `platform.export(name="day", rows=[{"a": 1}], destination="resources", key="d/o.csv")`, nil, exporter)
	require.NoError(t, err)
	assert.Empty(t, exporter.requests[0].Index, "unsaid is left to the deployment's rules")
}

func TestExport_AnIndexWithNoLibraryIsRefused(t *testing.T) {
	cases := map[string]struct{ call, want string }{
		"to the portal": {`platform.export(name="o", rows=[{"a": 1}], index="none")`, `destination="resources" only`},
		"a bad value":   {`platform.export(name="o", rows=[{"a": 1}], destination="resources", key="d/o.csv", index="most")`, `index must be one of`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exporter := newScopedExporter()
			_, err := destinationRun(t, tc.call, nil, exporter)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, exporter.requests)
		})
	}
}
