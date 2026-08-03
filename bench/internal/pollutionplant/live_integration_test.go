package pollutionplant

// Live integration: plant a treatment into a running stack, prove another
// identity is handed it, then remediate it and prove what each channel
// carries afterwards. Skipped unless POLLUTION_LIVE_URL is set, so the
// module's own test run stays hermetic.
//
// This is the test that would catch what nothing downstream can see: a
// claim that is stored but never delivered turns a treatment arm into a
// clean-stack control silently, and a remediation that retracts nothing
// turns a recovery arm into a second treatment arm just as silently.
//
// It writes to the stack it runs against: run it on a bench arm whose
// database is disposable, never on a stack a measured run will reuse
// without a reset.

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// liveTimeout bounds each call against the live stack.
const liveTimeout = 60 * time.Second

// liveClient builds a client against the stack under test, or skips.
func liveClient(t *testing.T) *Client {
	t.Helper()
	base := os.Getenv("POLLUTION_LIVE_URL")
	if base == "" {
		t.Skip("set POLLUTION_LIVE_URL (and POLLUTION_LIVE_KEY) to run against a live pollution-study stack")
	}
	tgt := target.Target{BaseURL: base, Credential: os.Getenv("POLLUTION_LIVE_KEY")}
	return New(tgt, pool.Size, lifecycleapi.New(base, tgt.HTTPClient(liveTimeout)), liveTimeout,
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

// TestLivePlantAndRemediate plants the study's warehouse treatment, proves
// cross-identity reach, and rolls it back, checking the state after each
// step through the platform's own APIs.
func TestLivePlantAndRemediate(t *testing.T) {
	client := liveClient(t)
	tr, err := TreatmentByID("fiscal-boundary-wrong")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	ctx := context.Background()

	planted, err := client.Plant(ctx, Request{Treatment: tr, TeacherSeq: 200, WitnessSeq: 201})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	if !planted.InSearch && !planted.InSink {
		t.Fatal("the plant reported no reachable surface")
	}
	if planted.ChangesetID == "" {
		t.Fatal("the plant recorded no changeset, so no rollback arm could run")
	}

	remediated, err := client.Remediate(ctx, RemediateRequest{
		Remediation: RemediationRollback,
		Planted:     planted,
		Treatment:   tr,
		TeacherSeq:  200,
		WitnessSeq:  202,
	})
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !remediated.InsightRetracted {
		t.Errorf("the rolled-back claim is still at status %q", remediated.InsightStatus)
	}
	if remediated.InSearch || remediated.InSink {
		t.Errorf("the rolled-back claim is still reachable: %+v", remediated)
	}
}

// TestLiveSupersedeRetractsTheInsight exercises the other mechanism, whose
// retraction is partial by design: the insight stops being delivered and
// the applied change stays applied. The residual state is recorded rather
// than asserted away, because it is what RQ3 measures.
func TestLiveSupersedeRetractsTheInsight(t *testing.T) {
	client := liveClient(t)
	tr, err := TreatmentByID("fiscal-boundary-wrong")
	if err != nil {
		t.Fatalf("treatment: %v", err)
	}
	correction, err := Counterpart(tr)
	if err != nil {
		t.Fatalf("counterpart: %v", err)
	}
	ctx := context.Background()

	planted, err := client.Plant(ctx, Request{Treatment: tr, TeacherSeq: 210, WitnessSeq: 211})
	if err != nil {
		t.Fatalf("plant: %v", err)
	}
	remediated, err := client.Remediate(ctx, RemediateRequest{
		Remediation: RemediationSupersede,
		Planted:     planted,
		Treatment:   tr,
		Correction:  correction,
		TeacherSeq:  210,
		WitnessSeq:  212,
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if remediated.InsightStatus != statusSuperseded {
		t.Errorf("the restated claim is at status %q, not superseded; the recall-first check did not match it",
			remediated.InsightStatus)
	}
	if remediated.CorrectionInsightID == "" {
		t.Error("no corrective capture was recorded")
	}
	t.Logf("after supersede: search=%v entity=%v", remediated.InSearch, remediated.InSink)
}
