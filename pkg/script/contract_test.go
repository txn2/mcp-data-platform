package script

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approvedAt is the approval stamp every contract test reuses.
var approvedAt = time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)

// liveScript returns an approved, in-service script whose live record has
// drifted from the approved version, which is the case the contract has to get
// right.
func liveScript() *Script {
	return &Script{
		ID: "script_1", Name: "daily-sales", DisplayName: "Daily Sales",
		Description: "Yesterday's sales by region",
		Scope:       ScopeGlobal, OwnerEmail: "jane@example.com", Enabled: true,
		Status:            StatusActive,
		ApprovedVersionID: "sver_3",
		Params:            []Param{{Name: "draft_only", Type: ParamTypeString}},
	}
}

// approvedVersion returns the version behind the gate, carrying a parameter
// contract the live record no longer has.
func approvedVersion() *Version {
	return &Version{
		ID: "sver_3", ScriptID: "script_1", Version: 3,
		Status: VersionStatusApplied, ApprovedBy: "admin@example.com", ApprovedAt: &approvedAt,
		Params: []Param{{Name: "report_date", Type: ParamTypeDate, Required: true}},
		Grants: Grants{Connections: []string{"warehouse"}},
	}
}

// TestBuildContractReportsTheApprovedParameterContract proves the contract
// describes what a run would actually bind against. A caller reads it to decide
// what to pass to run_script, and run_script executes the APPROVED version, so
// reporting the live record's edited parameters would hand the caller arguments
// the execution path rejects.
func TestBuildContractReportsTheApprovedParameterContract(t *testing.T) {
	c := BuildContract(liveScript(), approvedVersion(), nil, nil)

	require.Len(t, c.Params, 1)
	assert.Equal(t, "report_date", c.Params[0].Name, "params must come from the approved version")
	assert.True(t, c.Approval.Approved)
	assert.Equal(t, 3, c.Approval.Version)
	assert.Equal(t, "admin@example.com", c.Approval.ApprovedBy)
	assert.Empty(t, c.Approval.Refusal, "an approved, enabled, active script is runnable")
}

// TestBuildContractFallsBackToTheLiveParameterContract proves an unapproved
// script still reports parameters: an author preparing one needs to see the
// contract they are proposing, even though nothing will execute it yet.
func TestBuildContractFallsBackToTheLiveParameterContract(t *testing.T) {
	sc := liveScript()
	sc.ApprovedVersionID = ""
	sc.Status = StatusDraft

	c := BuildContract(sc, nil, nil, nil)

	require.Len(t, c.Params, 1)
	assert.Equal(t, "draft_only", c.Params[0].Name)
	assert.False(t, c.Approval.Approved)
	assert.Zero(t, c.Approval.Version)
	assert.Contains(t, c.Approval.Refusal, "no approved version")
}

// TestBuildContractReportsAGateRefusalOnAnApprovedScript proves the refusal is
// the gate's own answer rather than a re-reading of it: a disabled script has an
// approved version and still will not run, and a contract that reported only
// "approved" would tell a caller to run something run_script refuses.
func TestBuildContractReportsAGateRefusalOnAnApprovedScript(t *testing.T) {
	sc := liveScript()
	sc.Enabled = false

	c := BuildContract(sc, approvedVersion(), nil, nil)

	assert.True(t, c.Approval.Approved, "the version is still approved")
	assert.Equal(t, "the script is disabled", c.Approval.Refusal)
	assert.Contains(t, c.Text(), "A run requested now would be refused: the script is disabled.")
}

// TestBuildContractRefusesAnUnreadableApprovedVersion proves a live row
// pointing at a version the caller could not read is never reported as
// runnable. Promising execution against a version nothing has seen is worse
// than saying the read failed.
func TestBuildContractRefusesAnUnreadableApprovedVersion(t *testing.T) {
	c := BuildContract(liveScript(), nil, nil, nil)

	assert.False(t, c.Approval.Approved)
	assert.Contains(t, c.Approval.Refusal, "could not be read")
}

// TestRefuseNewRunRejectsAMismatchedVersion proves the gate refuses when the
// version handed to it is not the one the execution pointer names, rather than
// reporting a runnable script on the strength of some other approved version.
func TestRefuseNewRunRejectsAMismatchedVersion(t *testing.T) {
	other := approvedVersion()
	other.ID = "sver_9"

	err := RefuseNewRun(liveScript(), other)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be read")
}

// TestRefuseNewRunRejectsAMissingScript proves the nil case is an answer, not a
// panic: every discovery surface asks the gate about whatever it just read.
func TestRefuseNewRunRejectsAMissingScript(t *testing.T) {
	require.Error(t, RefuseNewRun(nil, nil))
}

// TestContractScheduleReportsCadenceAndNextFire proves an enabled schedule
// reports its next fire and a disabled one does not: a stored due time on a
// disabled schedule is a leftover, and printing it would predict a fire that
// will never happen.
func TestContractScheduleReportsCadenceAndNextFire(t *testing.T) {
	next := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	sched := &Schedule{
		CronSpec: "0 7 * * 1-5", Timezone: "America/Los_Angeles",
		Enabled: true, NextRunAt: next,
	}

	c := BuildContract(liveScript(), approvedVersion(), sched, nil)
	require.NotNil(t, c.Schedule)
	require.NotNil(t, c.Schedule.NextRunAt)
	assert.Equal(t, next, *c.Schedule.NextRunAt)
	assert.Contains(t, c.Text(), "next fire 2026-08-17 14:00 UTC")

	sched.Enabled = false
	c = BuildContract(liveScript(), approvedVersion(), sched, nil)
	require.NotNil(t, c.Schedule)
	assert.Nil(t, c.Schedule.NextRunAt)
	assert.Contains(t, c.Text(), "disabled")

	// An enabled schedule whose expression has nothing left to fire says so,
	// rather than reading as a cadence that simply has not fired yet.
	sched.Enabled = true
	sched.NextRunAt = time.Time{}
	c = BuildContract(liveScript(), approvedVersion(), sched, nil)
	require.NotNil(t, c.Schedule)
	assert.Nil(t, c.Schedule.NextRunAt)
	assert.Contains(t, c.Text(), "no further fire")
}

// TestContractNamesEachOutputShape proves "what did this produce" answers in
// two shapes since external delivery (#1288): a portal asset the platform still
// serves, and an object it wrote to a bucket and does not hold. A caller that
// could not tell them apart would go looking for an asset that does not exist.
func TestContractNamesEachOutputShape(t *testing.T) {
	finished := time.Date(2026, 8, 14, 7, 5, 0, 0, time.UTC)
	run := &Run{
		ID: "run_1", Version: 3, Status: RunStatusSucceeded, FinishedAt: &finished,
		Outputs: []RunOutput{
			{Name: "sales", AssetID: "asset_7", AssetVersion: 4, Format: "csv"},
			{Name: "sales", Destination: "acme-drop", Bucket: "acme-exports", Key: "2026/08/sales.csv", Format: "csv"},
		},
	}

	c := BuildContract(liveScript(), approvedVersion(), nil, run)

	require.NotNil(t, c.LastRun)
	require.Len(t, c.LastRun.Outputs, 2)
	assert.Equal(t, OutputKindAsset, c.LastRun.Outputs[0].Kind)
	assert.Equal(t, DestinationPortal, c.LastRun.Outputs[0].Destination,
		"an output with no recorded destination is a portal write")
	assert.Equal(t, OutputKindObject, c.LastRun.Outputs[1].Kind)
	assert.Equal(t, "acme-drop", c.LastRun.Outputs[1].Destination)

	text := c.Text()
	assert.Contains(t, text, "portal asset asset_7 v4")
	assert.Contains(t, text, "object acme-exports/2026/08/sales.csv delivered to acme-drop")
}

// TestContractTextReportsNeverHavingRun proves the absence of a run is stated
// rather than omitted: "no output recorded" and "no section about output" read
// very differently to an agent deciding whether to trust the automation.
func TestContractTextReportsNeverHavingRun(t *testing.T) {
	text := BuildContract(liveScript(), approvedVersion(), nil, nil).Text()

	assert.Contains(t, text, "Last successful run: none")
	assert.Contains(t, text, "Approval: version 3, approved by admin@example.com.")
	assert.Contains(t, text, "Parameters: report_date (required)")
}

// TestContractTextOmitsTheSource proves the document a reference resolves to
// never carries the script's code. Discovery answers "what is this and can I
// use it"; the code is what a reviewer reads.
func TestContractTextOmitsTheSource(t *testing.T) {
	sc := liveScript()
	sc.Source = "SECRET_MARKER = 1"

	assert.NotContains(t, BuildContract(sc, approvedVersion(), nil, nil).Text(), "SECRET_MARKER")
}

// TestContractTitleFallsBackToName proves a script authored without a display
// name still names itself by the name an agent would call it by.
func TestContractTitleFallsBackToName(t *testing.T) {
	sc := liveScript()
	sc.DisplayName = ""

	assert.Equal(t, "daily-sales", BuildContract(sc, approvedVersion(), nil, nil).Title())
}

// TestBuildContractOfNothing proves the nil script is an empty document rather
// than a panic, since callers pass whatever their store returned.
func TestBuildContractOfNothing(t *testing.T) {
	assert.Empty(t, BuildContract(nil, nil, nil, nil).ID)
}

// TestParamSummaryMarksRequiredParameters proves the one-line contract an agent
// reads from a search hit distinguishes what it MUST pass from what it may.
func TestParamSummaryMarksRequiredParameters(t *testing.T) {
	assert.Empty(t, ParamSummary(nil))
	assert.Equal(t, "report_date (required), region", ParamSummary([]Param{
		{Name: "report_date", Required: true},
		{Name: "region"},
	}))
}

// TestContractVisibilityMatchesTheRecord proves the contract answers the
// visibility question exactly as the script record does. The fetch path holds
// only the contract, and a second, drifting rule there would be a scope leak
// that no test of the record would catch.
func TestContractVisibilityMatchesTheRecord(t *testing.T) {
	cases := []struct {
		name     string
		scope    string
		personas []string
		owner    string
		email    string
		callerP  []string
		want     bool
	}{
		{"global reaches everyone", ScopeGlobal, nil, "jane@example.com", "", nil, true},
		{"persona reaches a member", ScopePersona, []string{"analyst"}, "", "bob@example.com", []string{"analyst"}, true},
		{"persona refuses a non-member", ScopePersona, []string{"analyst"}, "", "bob@example.com", []string{"engineer"}, false},
		{"persona refuses no membership at all", ScopePersona, []string{"analyst"}, "", "bob@example.com", nil, false},
		{"personal reaches its owner", ScopePersonal, nil, "jane@example.com", "jane@example.com", nil, true},
		{"personal refuses everyone else", ScopePersonal, nil, "jane@example.com", "bob@example.com", nil, false},
		{"personal refuses an unidentified caller", ScopePersonal, nil, "jane@example.com", "", nil, false},
		{"an ownerless personal script reaches nobody", ScopePersonal, nil, "", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &Script{Scope: tc.scope, Personas: tc.personas, OwnerEmail: tc.owner}
			c := BuildContract(sc, nil, nil, nil)

			assert.Equal(t, tc.want, c.VisibleToAny(tc.email, tc.callerP))
			assert.Equal(t, tc.want, sc.VisibleToAny(tc.email, tc.callerP),
				"the record and its contract must answer identically")
		})
	}
}

// TestVisibleToAnyMatchesVisibleToPerPersona proves the membership arity is the
// single-persona rule applied across a set, so the listing surface and the
// discovery surface cannot disagree about one persona.
func TestVisibleToAnyMatchesVisibleToPerPersona(t *testing.T) {
	sc := &Script{Scope: ScopePersona, Personas: []string{"analyst", "engineer"}}

	for _, persona := range []string{"analyst", "engineer", "auditor"} {
		assert.Equal(t, sc.VisibleTo("", persona), sc.VisibleToAny("", []string{persona}), persona)
	}
	assert.True(t, sc.VisibleToAny("", []string{"auditor", "engineer"}),
		"one matching membership is enough")
}

// TestContractTextIsOneDocument proves the rendered document leads with the
// title and carries each section on its own line, which is the shape both the
// fetch body and the prompt serve payload depend on.
func TestContractTextIsOneDocument(t *testing.T) {
	lines := strings.Split(BuildContract(liveScript(), approvedVersion(), nil, nil).Text(), "\n")

	require.GreaterOrEqual(t, len(lines), 4)
	assert.Equal(t, "Daily Sales", lines[0])
	assert.Equal(t, "Yesterday's sales by region", lines[1])
}

// TestContractRunWithoutOutputsSaysSo proves a successful run that wrote
// nothing is reported as such rather than as a run with an empty list, which
// reads as missing data.
func TestContractRunWithoutOutputsSaysSo(t *testing.T) {
	c := BuildContract(liveScript(), approvedVersion(), nil, &Run{ID: "run_2", Version: 3})

	require.NotNil(t, c.LastRun)
	assert.Nil(t, c.LastRun.Outputs)
	assert.Contains(t, c.Text(), "no recorded output")
}

// TestContractOutputOfUnknownShape proves an output that names neither an asset
// nor a bucket still reports its destination rather than rendering as blank.
func TestContractOutputOfUnknownShape(t *testing.T) {
	c := BuildContract(liveScript(), approvedVersion(), nil, &Run{
		ID: "run_3", Outputs: []RunOutput{{Name: "odd", Destination: "elsewhere"}},
	})

	require.NotNil(t, c.LastRun)
	assert.Empty(t, c.LastRun.Outputs[0].Kind)
	assert.Contains(t, c.Text(), "destination elsewhere")
}

// TestEffectiveSearchLimitIsClamped proves one source cannot skew a federated
// search by returning an unbounded candidate list.
func TestEffectiveSearchLimitIsClamped(t *testing.T) {
	assert.Equal(t, DefaultSearchLimit, SearchQuery{}.EffectiveLimit())
	assert.Equal(t, DefaultSearchLimit, SearchQuery{Limit: -1}.EffectiveLimit())
	assert.Equal(t, DefaultSearchLimit, SearchQuery{Limit: maxSearchLimit + 1}.EffectiveLimit())
	assert.Equal(t, 5, SearchQuery{Limit: 5}.EffectiveLimit())
}
