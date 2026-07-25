package pkrun

// The runner's contract. What matters here is the order of the sequence
// (the world must move after the belief is planted, or nothing is stale)
// and that a run refuses conditions under which its numbers would not mean
// what they say.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/pkcell"
	"github.com/txn2/mcp-data-platform/bench/internal/pkplant"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// fakeRunner records the world each episode was asked in.
type fakeRunner struct {
	fixture *fixturectl.Client
	worlds  []string
	answer  string
	err     error
}

func (f *fakeRunner) Model() string { return "fake-model" }

func (f *fakeRunner) Run(ctx context.Context, _ claudecli.Request) (claudecli.Result, error) {
	if f.err != nil {
		return claudecli.Result{}, f.err
	}
	w, err := f.fixture.World(ctx)
	if err != nil {
		return claudecli.Result{}, err
	}
	f.worlds = append(f.worlds, w.Profile)
	return claudecli.Result{
		FinalText: f.answer, ServerConnected: true, Handle: "dps_x", MCPCalls: 3,
		PlatformVersion: "v1.2.3",
		Transcript:      []llm.Message{{Role: "user", Text: "q"}},
	}, nil
}

// fakePlanter records the world each plant happened in, which is how the
// ordering property is checked: a belief must be planted in the world it
// describes, before the world moves.
type fakePlanter struct {
	fixture *fixturectl.Client
	worlds  []string
	err     error
}

func (f *fakePlanter) Plant(ctx context.Context, req pkplant.Request) (pkplant.Result, error) {
	if f.err != nil {
		return pkplant.Result{}, f.err
	}
	w, err := f.fixture.World(ctx)
	if err != nil {
		return pkplant.Result{}, err
	}
	f.worlds = append(f.worlds, w.Profile)
	return pkplant.Result{InsightID: "ins-1", Text: req.Seed.Text, Probed: req.Probe != ""}, nil
}

type fakeReader struct {
	held int
	err  error
}

func (f *fakeReader) ListInsights(context.Context, lifecycleapi.InsightFilter) ([]lifecycleapi.Insight, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]lifecycleapi.Insight, f.held)
	return out, nil
}

// newRun wires a run against a real perishable fixture.
func newRun(t *testing.T, answer string) (Options, *fakeRunner, *fakePlanter) {
	t.Helper()
	srv := httptest.NewServer(apisvc.New(apisvc.Options{Surface: apisvc.SurfacePerishable}))
	t.Cleanup(srv.Close)
	fix := fixturectl.New(srv.URL, "", 10*time.Second)
	runner := &fakeRunner{fixture: fix, answer: answer}
	planter := &fakePlanter{fixture: fix}
	cells, err := pkcell.PreRunCells()
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Target: target.Target{BaseURL: srv.URL, Credential: "k"}, IdentityKeys: 150,
		Fixture: fix, Planter: planter, Runner: runner, Cells: cells, K: 1,
		OutDir: t.TempDir(),
	}, runner, planter
}

// TestWorldMovesAfterThePlant is the property the whole study depends on:
// a belief is planted in the world it describes, and the question is asked
// in a world that may have moved. Reverse the order and nothing is ever
// stale.
func TestWorldMovesAfterThePlant(t *testing.T) {
	opts, runner, planter := newRun(t, "FINAL ANSWER: UNAVAILABLE")
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Attempts) != len(opts.Cells) {
		t.Fatalf("attempts = %d, want %d", len(res.Attempts), len(opts.Cells))
	}
	for i, c := range opts.Cells {
		if planter.worlds[i] != c.CaptureWorld {
			t.Errorf("%s: planted in world %q, want the belief's world %q", c.ID, planter.worlds[i], c.CaptureWorld)
		}
		if runner.worlds[i] != c.QueryWorld {
			t.Errorf("%s: asked in world %q, want %q", c.ID, runner.worlds[i], c.QueryWorld)
		}
	}
	// The stale cell really is stale, and the fresh one is not: the pre-run
	// pair brackets the axis it exists to estimate.
	var stale, fresh int
	for _, a := range res.Attempts {
		if a.Stale {
			stale++
		} else {
			fresh++
		}
		if a.Delivered == "" {
			t.Errorf("%s archived no delivered belief", a.CellID)
		}
	}
	if stale != 1 || fresh != 1 {
		t.Errorf("pre-run produced %d stale and %d fresh attempts, want one of each", stale, fresh)
	}
	if res.Manifest.SeedHash == "" || res.Manifest.PlatformVersion != "v1.2.3" {
		t.Errorf("manifest is incomplete: %+v", res.Manifest)
	}
}

// TestArchiveRoundTrips checks a run's results and transcripts land on disk
// in a form a later analysis can read.
func TestArchiveRoundTrips(t *testing.T) {
	opts, _, _ := newRun(t, "FINAL ANSWER: UNAVAILABLE")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(opts.OutDir, "results.json"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	var round Results
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("archive does not round-trip: %v", err)
	}
	if len(round.Attempts) == 0 || len(round.Cells) == 0 {
		t.Error("the archive carries no attempts or no cells")
	}
	entries, err := os.ReadDir(filepath.Join(opts.OutDir, "transcripts"))
	if err != nil || len(entries) == 0 {
		t.Errorf("no transcripts archived: %v", err)
	}
}

// TestPreflightRefusesCarriedOverKnowledge checks a run will not start
// against identities that already hold notes, because the agent would find
// two beliefs and the results would not say which it acted on.
func TestPreflightRefusesCarriedOverKnowledge(t *testing.T) {
	opts, runner, _ := newRun(t, "FINAL ANSWER: UNAVAILABLE")
	opts.Insights = &fakeReader{held: 1}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("run started against contaminated identities")
	} else if !strings.Contains(err.Error(), "already holds") {
		t.Errorf("error %q does not name the contamination", err)
	}
	if len(runner.worlds) != 0 {
		t.Error("the preflight spent attempts before refusing")
	}
	// A clean store lets the run proceed.
	opts.Insights = &fakeReader{}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Errorf("a clean store was refused: %v", err)
	}
}

// TestRunRefusesUnrunnableOptions checks the run will not start where its
// numbers could not mean what they say.
func TestRunRefusesUnrunnableOptions(t *testing.T) {
	base, _, _ := newRun(t, "x")
	cases := map[string]func(*Options){
		"no runner":     func(o *Options) { o.Runner = nil },
		"no planter":    func(o *Options) { o.Planter = nil },
		"no fixture":    func(o *Options) { o.Fixture = nil },
		"no cells":      func(o *Options) { o.Cells = nil },
		"no replicates": func(o *Options) { o.K = 0 },
		"no output":     func(o *Options) { o.OutDir = "" },
		// A pool too small for the matrix would silently reuse identities,
		// which is the contamination the preflight exists to prevent.
		"pool too small": func(o *Options) { o.IdentityKeys = 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := base
			mutate(&opts)
			if _, err := Run(context.Background(), opts); err == nil {
				t.Error("run accepted an unrunnable configuration")
			}
		})
	}
}

// TestFailuresAreRecordedNotCounted checks a harness failure lands on the
// attempt as an error rather than as a result.
func TestFailuresAreRecordedNotCounted(t *testing.T) {
	opts, _, planter := newRun(t, "x")
	planter.err = errors.New("plant exploded")
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, a := range res.Attempts {
		if a.Error == "" {
			t.Errorf("%s recorded no error despite a failed plant", a.CellID)
		}
		if a.Outcome.Correct != nil {
			t.Errorf("%s carries a verdict despite failing", a.CellID)
		}
	}
}

// TestScaffoldLicensesTrusting guards the correction that made the primary
// measure meaningful: the scaffold must permit answering from saved
// knowledge, or an agent that trusts its note is violating an instruction
// and the study measures obedience instead of the verification decision.
func TestScaffoldLicensesTrusting(t *testing.T) {
	if !strings.Contains(System, "knowledge saved in an earlier session") {
		t.Error("the scaffold no longer licenses answering from saved knowledge")
	}
	for _, banned := range []string{"verify", "re-check", "recheck", "confirm", "double-check"} {
		if strings.Contains(strings.ToLower(System), banned) {
			t.Errorf("the scaffold contains %q and would instruct the measured action", banned)
		}
	}
}
