package scriptrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/exporttable"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// registeringCaller answers manage_table register the way the portal toolkit
// does, and records what it was asked.
type registeringCaller struct {
	calls []recordedCall
	err   error
}

func (c *registeringCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	c.calls = append(c.calls, recordedCall{name: name, args: args})
	if c.err != nil {
		return nil, c.err
	}
	return map[string]any{
		"registration_id": "reg_1", "connection": args["connection"],
		"query_table": "scratch.uploads.analyst_orders", "columns": []any{"id", "note"},
		"format": "jsonl", "follow": args["follow"],
	}, nil
}

// registerRun executes source against a library-landing exporter, or none.
func registerRun(t *testing.T, source string, caller Caller, exporter Exporter, writes WriteBarrier) (*Result, error) {
	t.Helper()
	opts := Options{
		Source: source, Name: "test", RunID: "dpx_1", FireTime: fireTime,
		Caller: caller, Writes: writes,
		Destinations: []script.Destination{acmeDrop()},
	}
	if exporter != nil {
		opts.Exporter = exporter
	}
	return Run(context.Background(), opts)
}

const registerSource = `out = platform.export(name="orders", rows=[{"id": 1, "note": "a\nb"}], format="jsonl",
    destination="resources", key="staging/orders.jsonl",
    register={"connection": "warehouse", "table_name": "orders"})
print(out["table"]["query_table"], out["table"]["columns"])
`

// TestExport_RegisterMakesTheTableOverTheWrittenFile is #1820's one call: the
// file is written, then registered through manage_table by the reference the
// write reported, and the script is handed the table to query.
func TestExport_RegisterMakesTheTableOverTheWrittenFile(t *testing.T) {
	caller := &registeringCaller{}
	exporter := newLandingExporter()
	result, err := registerRun(t, registerSource, caller, exporter, WritesMade)
	require.NoError(t, err)

	require.Len(t, exporter.requests, 1)
	assert.Equal(t, "jsonl", exporter.requests[0].Format)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, "manage_table", caller.calls[0].name)
	assert.Equal(t, map[string]any{
		"action": "register", "reference": "mcp:resource:res-1", "connection": "warehouse",
		"table_name": "orders", "follow": true,
	}, caller.calls[0].args)

	require.Len(t, result.Exports, 1)
	table := result.Exports[0].Table
	require.NotNil(t, table)
	assert.Equal(t, "scratch.uploads.analyst_orders", table.QueryTable)
	assert.Equal(t, []string{"id", "note"}, table.Columns)
	assert.Contains(t, result.Log, `scratch.uploads.analyst_orders ["id", "note"]`)
	assert.Empty(t, result.Writes, "a platform run records no write list")
}

// TestExport_RegisterInAWriteReportingDraftIsListed: a draft allowed to write
// makes the registration and lists it beside the other writes.
func TestExport_RegisterInAWriteReportingDraftIsListed(t *testing.T) {
	result, err := registerRun(t, registerSource, &registeringCaller{}, newLandingExporter(), WritesReported)
	require.NoError(t, err)
	assert.Equal(t, []WriteRecord{{Tool: "manage_table", Call: "manage_table action=register"}}, result.Writes)
}

// TestExport_RegisterInAPreviewReportsTheTableItWouldMake: with no exporter
// nothing was written, so there is no file to register and no call is made.
func TestExport_RegisterInAPreviewReportsTheTableItWouldMake(t *testing.T) {
	caller := &registeringCaller{}
	source := `out = platform.export(name="orders", rows=[{"id": 1}], format="jsonl",
    destination="resources", key="staging/orders.jsonl", register={"connection": "warehouse"})
print(out["table"]["preview"], out["table"].get("query_table", "none"))
`
	result, err := registerRun(t, source, caller, nil, WritesRefused)
	require.NoError(t, err)
	assert.Empty(t, caller.calls)
	require.Len(t, result.Exports, 1)
	assert.Equal(t, &exporttable.Table{Connection: "warehouse", Follow: true, Preview: true}, result.Exports[0].Table)
	assert.Contains(t, result.Log, "True none", "a preview names no table, because none was made")
}

// TestExport_RegisterFailureFailsTheRunNamingTheOutput: the file was written
// and the table was not, and the next step of a pipeline would query a table
// that is not there, so the run stops here.
func TestExport_RegisterFailureFailsTheRunNamingTheOutput(t *testing.T) {
	caller := &registeringCaller{err: errors.New("line 2: the value of \"x\" is a nested object or list")}
	_, err := registerRun(t, registerSource, caller, newLandingExporter(), WritesMade)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output "orders" was written, and registering it as a table failed`)
	assert.Contains(t, err.Error(), "nested object")
}

// TestExport_RegisterRefusalsPrecedeTheWrite: what register= cannot do is
// refused before anything is written.
func TestExport_RegisterRefusalsPrecedeTheWrite(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			"a format no table reads",
			`platform.export(name="o", rows=[{"a": 1}], format="json", destination="resources", key="s/o.json", register={"connection": "w"})`,
			`register needs format="jsonl", "parquet" or "csv"`,
		},
		{
			"a bucket destination",
			`platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="acme-drop", register={"connection": "w"})`,
			"a bucket destination delivers the file out of the platform",
		},
		{
			"no connection",
			`platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/o.jsonl", register={})`,
			"register needs a connection",
		},
		{
			"an unknown key",
			`platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/o.jsonl", register={"connection": "w", "repair": True})`,
			`register has no key "repair"`,
		},
		{
			"not a dict",
			`platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/o.jsonl", register="w")`,
			"register must be a dict",
		},
		{
			"a non-string table name",
			`platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/o.jsonl", register={"connection": "w", "table_name": 3})`,
			"register table_name must be a string",
		},
		{
			"a non-bool follow",
			`platform.export(name="o", rows=[{"a": 1}], format="jsonl", destination="resources", key="s/o.jsonl", register={"connection": "w", "follow": "yes"})`,
			"register follow must be True or False",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := newLandingExporter()
			_, err := registerRun(t, tt.source, &registeringCaller{}, exporter, WritesMade)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, exporter.requests, "the refusal precedes the write")
		})
	}
}

// TestExport_RegisterOverAPortalOutputNamesTheAsset: a portal output is
// registered by the asset's reference, and follow=False pins it.
func TestExport_RegisterOverAPortalOutputNamesTheAsset(t *testing.T) {
	caller := &registeringCaller{}
	_, err := registerRun(t,
		`platform.export(name="o", rows=[{"a": 1}], format="csv", register={"connection": "w", "follow": False})`,
		caller, &recordingExporter{}, WritesMade)
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, "mcp:asset:asset_1", caller.calls[0].args["reference"])
	assert.Equal(t, false, caller.calls[0].args["follow"])
	_, named := caller.calls[0].args["table_name"]
	assert.False(t, named, "no table_name takes manage_table's default")
}

// TestValidate_ReportsWhatRegisterReaches: register= is a manage_table call on
// the connection it names, and validate reports both, or says the connection
// was computed.
func TestValidate_ReportsWhatRegisterReaches(t *testing.T) {
	report := Validate(`platform.export(name="o", rows=[], format="jsonl", destination="resources",
    key="s/o.jsonl", register={"connection": "warehouse"})`)
	assert.True(t, report.OK, report.Findings)
	assert.Equal(t, []string{"manage_table"}, report.Tools)
	assert.Equal(t, []string{"warehouse"}, report.Connections)
	assert.False(t, report.DynamicConnections)

	report = Validate(`spec = {"connection": "warehouse"}
platform.export(name="o", rows=[], format="jsonl", destination="resources", key="s/o.jsonl", register=spec)`)
	assert.Equal(t, []string{"manage_table"}, report.Tools)
	assert.True(t, report.DynamicConnections, "a computed register= hides its connection, and the report says so")
}
