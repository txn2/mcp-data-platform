package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pollutionplant"
)

func TestRunRejectsAnUnknownMode(t *testing.T) {
	if err := run(config{mode: "not-a-mode"}); err == nil {
		t.Error("an unknown -mode was accepted")
	}
}

// Every stack-side mode needs a credential, and saying so up front beats a
// transport error halfway through a plant.
func TestStackSideModesRequireACredential(t *testing.T) {
	for _, mode := range []string{"plant", "remediate", "seed-correct-source", "store-state"} {
		t.Run(mode, func(t *testing.T) {
			if err := run(config{mode: mode}); err == nil {
				t.Error("mode ran without a credential")
			}
		})
	}
}

func TestTableRendersTheCommittedMatrix(t *testing.T) {
	if err := run(config{mode: "table"}); err != nil {
		t.Fatalf("table: %v", err)
	}
}

func TestCheckAgainstTheCommittedTasks(t *testing.T) {
	if err := run(config{mode: "check", tasksDir: filepath.Join("..", "tasks")}); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestReadStoreStateRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre.json")
	want := pollutionplant.StoreState{
		Insights: []pollutionplant.InsightState{{ID: "in-1", Status: "applied", CapturedBy: "a@b"}},
	}
	raw := `{"insights":[{"id":"in-1","status":"applied","captured_by":"a@b"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readStoreState(path)
	if err != nil {
		t.Fatalf("readStoreState: %v", err)
	}
	if len(got.Insights) != 1 || got.Insights[0] != want.Insights[0] {
		t.Errorf("round trip lost the snapshot: %+v", got)
	}
}

func TestReadStoreStateSurfacesBadInput(t *testing.T) {
	if _, err := readStoreState(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing snapshot was accepted")
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readStoreState(path)
	if err == nil {
		t.Fatal("a malformed snapshot was accepted")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error does not say the file was unparseable: %v", err)
	}
}

func TestReadPlantedRequiresAPath(t *testing.T) {
	if _, err := readPlanted(""); err == nil {
		t.Error("a remediation without -planted was accepted")
	}
}

// fakeStack stands in for the platform for the two modes whose decision
// logic, rather than whose plumbing, is what matters.
type fakeStack struct {
	state    pollutionplant.StoreState
	stateErr error
	page     lifecycleapi.KnowledgePage
	pageErr  error
}

func (f *fakeStack) ReadStoreState(context.Context) (pollutionplant.StoreState, error) {
	return f.state, f.stateErr
}

func (f *fakeStack) SeedCorrectSource(context.Context) (lifecycleapi.KnowledgePage, error) {
	return f.page, f.pageErr
}

func snapshotFile(t *testing.T, s pollutionplant.StoreState) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pre.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func state(status string) pollutionplant.StoreState {
	return pollutionplant.StoreState{
		Insights: []pollutionplant.InsightState{{ID: "in-1", Status: status, CapturedBy: "a@b"}},
	}
}

// With no pre-arm snapshot the mode only archives: the first call of an arm
// has nothing to compare against and must not fail for it.
func TestCheckStoreStateArchivesWithoutABaseline(t *testing.T) {
	var out, status strings.Builder
	if err := checkStoreState(context.Background(), &fakeStack{state: state("applied")}, "", &out, &status); err != nil {
		t.Fatalf("checkStoreState: %v", err)
	}
	if !strings.Contains(out.String(), `"in-1"`) {
		t.Errorf("the snapshot was not archived: %s", out.String())
	}
	if status.String() != "" {
		t.Errorf("a baseline-free call reported a verdict: %s", status.String())
	}
}

func TestCheckStoreStatePassesOnAConstantStore(t *testing.T) {
	var out, status strings.Builder
	err := checkStoreState(context.Background(), &fakeStack{state: state("applied")},
		snapshotFile(t, state("applied")), &out, &status)
	if err != nil {
		t.Fatalf("a constant store failed the invariant: %v", err)
	}
	if !strings.Contains(status.String(), "constant") {
		t.Errorf("no verdict reported: %s", status.String())
	}
}

// The arm-invalidating case. The snapshot must still be archived: an arm that
// has to be re-run is worth understanding, and the evidence of what moved is
// in the post-state.
func TestCheckStoreStateFailsAndStillArchivesOnDrift(t *testing.T) {
	var out, status strings.Builder
	err := checkStoreState(context.Background(), &fakeStack{state: state("rejected")},
		snapshotFile(t, state("applied")), &out, &status)
	if err == nil {
		t.Fatal("a store that moved during the arm was accepted")
	}
	if !strings.Contains(err.Error(), "fresh database") {
		t.Errorf("the error does not state the remedy: %v", err)
	}
	if !strings.Contains(status.String(), "CROSS-IDENTITY DRIFT") {
		t.Errorf("the drift was not reported: %s", status.String())
	}
	if !strings.Contains(out.String(), "rejected") {
		t.Errorf("the post-state was not archived: %s", out.String())
	}
}

func TestCheckStoreStateSurfacesReadAndBaselineFailures(t *testing.T) {
	var out, status strings.Builder
	if err := checkStoreState(context.Background(), &fakeStack{stateErr: errors.New("boom")}, "", &out, &status); err == nil {
		t.Error("a failed store read was reported as a clean snapshot")
	}
	err := checkStoreState(context.Background(), &fakeStack{state: state("applied")},
		filepath.Join(t.TempDir(), "missing.json"), &out, &status)
	if err == nil {
		t.Error("a missing baseline was accepted")
	}
}

func TestSeedCorrectSourceArchivesThePage(t *testing.T) {
	var out strings.Builder
	page := lifecycleapi.KnowledgePage{ID: "kp-1", Slug: pollutionplant.CorrectCoverageSourceSlug, Summary: "70 or higher"}
	if err := seedCorrectSource(context.Background(), &fakeStack{page: page}, &out); err != nil {
		t.Fatalf("seedCorrectSource: %v", err)
	}
	if !strings.Contains(out.String(), pollutionplant.CorrectCoverageSourceSlug) {
		t.Errorf("the seeded page was not archived: %s", out.String())
	}
}

func TestSeedCorrectSourceSurfacesFailure(t *testing.T) {
	var out strings.Builder
	if err := seedCorrectSource(context.Background(), &fakeStack{pageErr: errors.New("boom")}, &out); err == nil {
		t.Error("a failed seed was reported as a success")
	}
}

// An evaluator's own pending capture is recorded and does not fail the arm:
// nobody else can read it, so no later episode met a different store. The
// count still has to reach the operator, because how often evaluators write to
// a shared store is a finding in a study about shared stores.
func TestCheckStoreStateRecordsButAllowsAnOwnAuthorWrite(t *testing.T) {
	var out, status strings.Builder
	before := pollutionplant.StoreState{
		Insights: []pollutionplant.InsightState{{ID: "in-1", Status: "applied", CapturedBy: "teacher@x"}},
	}
	after := pollutionplant.StoreState{
		Insights: []pollutionplant.InsightState{
			{ID: "in-1", Status: "applied", CapturedBy: "teacher@x"},
			{ID: "in-2", Status: "pending", CapturedBy: "bench-agent-015@apikey.local"},
		},
	}
	if err := checkStoreState(context.Background(), &fakeStack{state: after},
		snapshotFile(t, before), &out, &status); err != nil {
		t.Fatalf("an own-author pending write invalidated the arm: %v", err)
	}
	s := status.String()
	if !strings.Contains(s, "evaluator-write") || !strings.Contains(s, "bench-agent-015@apikey.local") {
		t.Errorf("the write was not recorded for the report: %s", s)
	}
	if strings.Contains(s, "CROSS-IDENTITY DRIFT") {
		t.Errorf("a pending own-author write was reported as cross-identity: %s", s)
	}
}
