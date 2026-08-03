package pollutionplant

// A remediation that reported success without retracting anything would be
// the worst failure this harness can have: the arm would run on the planted
// condition and the study would publish it as recovery. These tests drive
// each mechanism against a fake platform that retracts, does not retract,
// and retracts only partly.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
)

// remediable sets the fake up with a planted claim already applied.
func remediable(f *fakePlatform, tr Treatment) Result {
	f.insight["insight-1"] = lifecycleapi.Insight{ID: "insight-1", Status: "applied", ChangesetRef: "cs-1"}
	f.searchBody = "result carrying " + tr.Needle
	f.entityBody = `{"description":"` + tr.Text + `"}`
	return Result{TreatmentID: tr.ID, InsightID: "insight-1", ChangesetID: "cs-1", Text: tr.Text, Needle: tr.Needle}
}

// retracted marks the planted insight retracted and clears the surfaces
// named.
func retracted(f *fakePlatform, status string, clearSearch, clearEntity bool) {
	f.insight["insight-1"] = lifecycleapi.Insight{ID: "insight-1", Status: status, ChangesetRef: "cs-1"}
	if clearSearch {
		f.searchBody = "results with nothing planted in them"
	}
	if clearEntity {
		f.entityBody = `{"description":"the seeded description"}`
	}
}

// remediateReq builds a request for a mechanism.
func remediateReq(t *testing.T, r Remediation, tr Treatment, planted Result) RemediateRequest {
	t.Helper()
	req := RemediateRequest{Remediation: r, Planted: planted, Treatment: tr, TeacherSeq: 200, WitnessSeq: 202}
	if r == RemediationSupersede {
		correction, err := Counterpart(tr)
		if err != nil {
			t.Fatalf("counterpart: %v", err)
		}
		req.Correction = correction
	}
	return req
}

func TestRollbackRetractsBothChannels(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	planted := remediable(f, tr)
	retracted(f, "rolled_back", true, true)

	got, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationRollback, tr, planted))
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !got.InsightRetracted || got.InSearch || got.InSink {
		t.Errorf("rollback reported a claim still in force: %+v", got)
	}
	if n := f.open(); n != 0 {
		t.Errorf("the remediation left %d session(s) open", n)
	}
	// The revert has to go through the tool an operator would use, naming
	// the changeset the plant recorded and confirming it.
	var rollbacks int
	for _, c := range f.calls {
		if c.tool != applyTool {
			continue
		}
		rollbacks++
		if c.args["action"] != "rollback" || c.args["changeset_id"] != "cs-1" || c.args["confirm"] != true {
			t.Errorf("rollback called with %v", c.args)
		}
		if c.seq != AdminSeq {
			t.Errorf("rollback ran as identity %d, not the reviewer", c.seq)
		}
	}
	if rollbacks != 1 {
		t.Errorf("expected exactly one rollback call, got %d", rollbacks)
	}
}

func TestRollbackRefusesAPartialRevert(t *testing.T) {
	tr := wrongFiscal(t)
	cases := map[string]struct {
		status                   string
		clearSearch, clearEntity bool
		want                     string
	}{
		"sink still carries it":   {"rolled_back", true, false, "readable at its sink"},
		"search still carries it": {"rolled_back", false, true, "reachable through search"},
		"insight still delivered": {"applied", true, true, "still delivers"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform()
			planted := remediable(f, tr)
			retracted(f, tc.status, tc.clearSearch, tc.clearEntity)
			_, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationRollback, tr, planted))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("a partial revert was accepted (want %q): %v", tc.want, err)
			}
		})
	}
}

func TestSupersedeRestatesTheClaimAsItsCapturer(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	planted := remediable(f, tr)
	f.nextID = "insight-correction"
	retracted(f, "superseded", true, false)

	got, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationSupersede, tr, planted))
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if got.CorrectionInsightID != "insight-correction" {
		t.Errorf("the corrective capture was not recorded: %+v", got)
	}
	// The restatement check is scoped to the caller's own memory, so a
	// correction captured by anyone but the original capturer supersedes
	// nothing.
	var captures int
	for _, c := range f.calls {
		if c.tool != captureTool {
			continue
		}
		captures++
		if c.seq != 200 {
			t.Errorf("the correction was captured as identity %d, not the claim's capturer", c.seq)
		}
		if text, _ := c.args["content"].(string); !strings.Contains(text, "February 1") {
			t.Errorf("the correction did not carry the correct boundary: %q", text)
		}
	}
	if captures != 1 {
		t.Errorf("expected exactly one corrective capture, got %d", captures)
	}
}

// TestSupersedeRecordsSinkResidueRatherThanRefusingIt pins the asymmetry
// between the two mechanisms: supersede retracts the insight and leaves the
// applied change in place, and that residual state is what RQ3 measures, so
// the harness must report it rather than reject it.
func TestSupersedeRecordsSinkResidueRatherThanRefusingIt(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	planted := remediable(f, tr)
	retracted(f, "superseded", false, false)

	got, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationSupersede, tr, planted))
	if err != nil {
		t.Fatalf("supersede refused a state it is defined to leave behind: %v", err)
	}
	if !got.InsightRetracted {
		t.Error("the superseded insight was not reported retracted")
	}
	if !got.InSink || !got.InSearch {
		t.Errorf("the residue the applied change leaves was not recorded: %+v", got)
	}
}

func TestSupersedeRefusesWhenTheInsightStaysInForce(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	planted := remediable(f, tr)
	// The correction was not similar enough for the recall-first check to
	// match it, so the claim is still delivered.
	retracted(f, "applied", true, true)

	_, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationSupersede, tr, planted))
	if err == nil || !strings.Contains(err.Error(), "still delivers") {
		t.Fatalf("a supersede that superseded nothing was accepted: %v", err)
	}
}

func TestRemediateRefusesAnIncoherentRequest(t *testing.T) {
	tr := wrongFiscal(t)
	api, err := TreatmentByID("coverage-threshold-correct")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	planted := Result{InsightID: "insight-1", ChangesetID: "cs-1"}
	cases := map[string]RemediateRequest{
		"unknown mechanism": {Remediation: "forget-about-it", Planted: planted, Treatment: tr, TeacherSeq: 200, WitnessSeq: 202},
		"no insight":        {Remediation: RemediationRollback, Treatment: tr, TeacherSeq: 200, WitnessSeq: 202},
		"no changeset": {Remediation: RemediationRollback, Planted: Result{InsightID: "insight-1"},
			Treatment: tr, TeacherSeq: 200, WitnessSeq: 202},
		"no identities": {Remediation: RemediationRollback, Planted: planted, Treatment: tr},
		"correction is the wrong arm": {Remediation: RemediationSupersede, Planted: planted, Treatment: tr,
			Correction: tr, TeacherSeq: 200, WitnessSeq: 202},
		"correction is another claim": {Remediation: RemediationSupersede, Planted: planted, Treatment: tr,
			Correction: api, TeacherSeq: 200, WitnessSeq: 202},
		"no correction at all": {Remediation: RemediationSupersede, Planted: planted, Treatment: tr,
			TeacherSeq: 200, WitnessSeq: 202},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform()
			if _, err := f.client().Remediate(context.Background(), req); err == nil {
				t.Fatal("an incoherent remediation request was accepted")
			}
			if len(f.calls) != 0 {
				t.Error("the platform was touched before the request was checked")
			}
		})
	}
}

// TestRemediateSurfacesPlatformFailures keeps a broken platform from
// reading as a completed remediation: every step that can fail must fail
// the remediation rather than leave the arm mislabeled.
func TestRemediateSurfacesPlatformFailures(t *testing.T) {
	tr := wrongFiscal(t)
	cases := map[string]struct {
		mechanism Remediation
		setup     func(*fakePlatform, *Client)
	}{
		"rollback: reviewer session fails": {RemediationRollback, func(_ *fakePlatform, c *Client) {
			c.dial = func(context.Context, int) (session, error) { return nil, errors.New("no route to host") }
		}},
		"rollback: gate refused": {RemediationRollback, func(f *fakePlatform, _ *Client) {
			f.searchRefuses = true
		}},
		"rollback: transport": {RemediationRollback, func(f *fakePlatform, _ *Client) {
			f.rollbackChangeset = "" // the tool answers, but the transport does not
			f.entityErr = errors.New("connection reset")
		}},
		"supersede: capturing session fails": {RemediationSupersede, func(_ *fakePlatform, c *Client) {
			c.dial = func(context.Context, int) (session, error) { return nil, errors.New("no route to host") }
		}},
		"supersede: correction refused": {RemediationSupersede, func(f *fakePlatform, _ *Client) {
			f.captureRefuses = true
		}},
		"insight unreadable afterwards": {RemediationRollback, func(f *fakePlatform, _ *Client) {
			delete(f.insight, "insight-1")
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakePlatform()
			planted := remediable(f, tr)
			retracted(f, "rolled_back", true, true)
			client := f.client()
			tc.setup(f, client)
			if _, err := client.Remediate(context.Background(), remediateReq(t, tc.mechanism, tr, planted)); err == nil {
				t.Fatal("a remediation against a broken platform was reported as complete")
			}
		})
	}
}

func TestRollbackSurfacesARefusal(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	planted := remediable(f, tr)
	f.rollbackRefuses = true

	_, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationRollback, tr, planted))
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused rollback was reported as a remediation: %v", err)
	}
}

// TestRollbackRequiresAMatchingConfirmation guards the case where the tool
// answers successfully about some other changeset: nothing downstream would
// notice, and the planted change would still be live.
func TestRollbackRequiresAMatchingConfirmation(t *testing.T) {
	tr := wrongFiscal(t)
	f := newFakePlatform()
	planted := remediable(f, tr)
	f.rollbackChangeset = "cs-somebody-elses"

	_, err := f.client().Remediate(context.Background(), remediateReq(t, RemediationRollback, tr, planted))
	if err == nil || !strings.Contains(err.Error(), "no matching confirmation") {
		t.Fatalf("a rollback confirming another changeset was accepted: %v", err)
	}
}
