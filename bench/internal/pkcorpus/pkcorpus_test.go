package pkcorpus

// The corpus runner is exercised against the real fixture control plane and
// a stub admin API, with the episode runner faked: what matters here is
// that every episode observes its scenario's world, that captured prose is
// archived verbatim, and that a failing episode neither aborts the run nor
// loses the episodes around it.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// fakeRunner records the world each episode saw and returns a canned
// result. It reads the world through the fixture's own control plane, the
// same way a real episode would see it through the catalog.
type fakeRunner struct {
	fixture *fixturectl.Client
	worlds  []string
	result  claudecli.Result
	err     error
	failOn  int // 1-based episode index to fail, 0 for none
	calls   int
}

func (f *fakeRunner) Model() string { return "fake-model" }

func (f *fakeRunner) Run(ctx context.Context, _ claudecli.Request) (claudecli.Result, error) {
	f.calls++
	w, err := f.fixture.World(ctx)
	if err != nil {
		return claudecli.Result{}, err
	}
	f.worlds = append(f.worlds, w.Profile)
	if f.failOn == f.calls {
		return claudecli.Result{}, errors.New("episode blew up")
	}
	return f.result, f.err
}

// insightsStub serves the admin insights listing, echoing one captured
// insight per identity that asks. A listing with no `since` bound is a
// preflight probe and answers empty, matching a clean store; the run's own
// read-backs always carry the episode start.
func insightsStub(t *testing.T, text string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/insights") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("since") == "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total":0,"data":[]}`))
			return
		}
		email := r.URL.Query().Get("captured_by")
		body := map[string]any{"total": 1, "data": []map[string]any{{
			"id": "ins-" + email, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"captured_by": email, "category": "api_behavior",
			"insight_text": text, "status": "pending",
		}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// newRun wires a corpus run against a real perishable fixture.
func newRun(t *testing.T, text string) (Options, *fakeRunner) {
	t.Helper()
	fixtureSrv := httptest.NewServer(apisvc.New(apisvc.Options{Surface: apisvc.SurfacePerishable}))
	t.Cleanup(fixtureSrv.Close)
	fixture := fixturectl.New(fixtureSrv.URL, "", 10*time.Second)
	stub := insightsStub(t, text)
	runner := &fakeRunner{
		fixture: fixture,
		result: claudecli.Result{
			FinalText: "FINAL ANSWER: none", ServerConnected: true, Handle: "dps_x",
			PlatformVersion: "v1.2.3", MCPCalls: 4,
			Transcript: []llm.Message{{Role: "user", Text: "prompt"}},
		},
	}
	return Options{
		Target:       target.Target{BaseURL: stub.URL, Credential: "k"},
		IdentityKeys: 150,
		Fixture:      fixture,
		Insights:     lifecycleapi.New(stub.URL, stub.Client()),
		Runner:       runner,
		Replicates:   1,
		OutDir:       t.TempDir(),
		CaptureWait:  2 * time.Second,
	}, runner
}

// TestRunObservesEachScenarioWorld is the property the corpus depends on:
// a captured belief is only evidence about a state if the episode that
// captured it actually ran in that state.
func TestRunObservesEachScenarioWorld(t *testing.T) {
	opts, runner := newRun(t, "Listening is not usable: zero monitors provisioned.")
	corpus, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	scenarios := Scenarios()
	if len(corpus.Episodes) != len(scenarios) {
		t.Fatalf("episodes = %d, want %d", len(corpus.Episodes), len(scenarios))
	}
	for i, sc := range scenarios {
		if runner.worlds[i] != sc.World {
			t.Errorf("%s ran in world %q, want %q", sc.ID, runner.worlds[i], sc.World)
		}
		ep := corpus.Episodes[i]
		if ep.ScenarioID != sc.ID || ep.World != sc.World || ep.Class != sc.Class {
			t.Errorf("episode %d = %+v, want scenario %s", i, ep, sc.ID)
		}
		if ep.Error != "" {
			t.Errorf("%s: %s", sc.ID, ep.Error)
		}
		if len(ep.Captured) != 1 {
			t.Fatalf("%s captured %d insights, want 1", sc.ID, len(ep.Captured))
		}
		if ep.Captured[0].InsightText != "Listening is not usable: zero monitors provisioned." {
			t.Errorf("%s archived %q, not the stored text", sc.ID, ep.Captured[0].InsightText)
		}
		if ep.Captured[0].CapturedBy != ep.Email {
			t.Errorf("%s archived an insight from %q, want %q", sc.ID, ep.Captured[0].CapturedBy, ep.Email)
		}
	}
	// Identities are distinct across episodes: a shared credential would
	// fold one episode's capture into the next one's read-back.
	seen := map[string]bool{}
	for _, ep := range corpus.Episodes {
		if seen[ep.Email] {
			t.Errorf("identity %s reused", ep.Email)
		}
		seen[ep.Email] = true
	}
	if corpus.Manifest.CapturedTotal != len(scenarios) {
		t.Errorf("captured total = %d, want %d", corpus.Manifest.CapturedTotal, len(scenarios))
	}
	if corpus.Manifest.ScenariosHash != ScenariosHash() || corpus.System != System {
		t.Error("manifest does not name the stimulus that produced the corpus")
	}
	if corpus.Manifest.PlatformVersion != "v1.2.3" {
		t.Errorf("platform version = %q", corpus.Manifest.PlatformVersion)
	}
}

// TestRunArchivesEvenWhenEpisodesFail checks that one blown episode costs
// exactly one episode: the run continues and the archive still lands.
func TestRunArchivesEvenWhenEpisodesFail(t *testing.T) {
	opts, runner := newRun(t, "text")
	runner.failOn = 2
	corpus, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := corpus.Episodes[1].Error; got == "" {
		t.Error("the failed episode records no error")
	}
	if corpus.Episodes[0].Error != "" || corpus.Episodes[2].Error != "" {
		t.Error("a failure took its neighbors with it")
	}
	raw, err := os.ReadFile(filepath.Join(opts.OutDir, "corpus.json"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	var round Corpus
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("archive does not round-trip: %v", err)
	}
	if len(round.Episodes) != len(corpus.Episodes) {
		t.Errorf("archive holds %d episodes, run had %d", len(round.Episodes), len(corpus.Episodes))
	}
	if len(round.Scenarios) != len(Scenarios()) {
		t.Error("archive does not carry the scenario set")
	}
	entries, err := os.ReadDir(filepath.Join(opts.OutDir, "transcripts"))
	if err != nil || len(entries) == 0 {
		t.Errorf("no transcripts archived: %v", err)
	}
}

// TestRunRejectsUnrunnableOptions checks the run refuses before spending
// episodes on a configuration that cannot produce a usable corpus.
func TestRunRejectsUnrunnableOptions(t *testing.T) {
	base, _ := newRun(t, "text")
	cases := map[string]func(*Options){
		"no runner":      func(o *Options) { o.Runner = nil },
		"no fixture":     func(o *Options) { o.Fixture = nil },
		"no insights":    func(o *Options) { o.Insights = nil },
		"no replicates":  func(o *Options) { o.Replicates = 0 },
		"no identities":  func(o *Options) { o.IdentityKeys = 0 },
		"no output":      func(o *Options) { o.OutDir = "" },
		"unknown world":  func(o *Options) { o.Fixture = fixturectl.New("http://127.0.0.1:1", "", time.Second) },
		"still archives": func(*Options) {},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := base
			mutate(&opts)
			corpus, err := Run(context.Background(), opts)
			if name == "still archives" {
				if err != nil {
					t.Fatalf("baseline run failed: %v", err)
				}
				return
			}
			if name == "unknown world" {
				// A dead control plane is not a config error: the run
				// proceeds and every episode records the failure.
				if err != nil || corpus == nil {
					t.Fatalf("run aborted instead of recording: %v", err)
				}
				for _, ep := range corpus.Episodes {
					if ep.Error == "" {
						t.Errorf("%s recorded no error against a dead fixture", ep.ScenarioID)
					}
				}
				return
			}
			if err == nil {
				t.Error("run accepted an unrunnable configuration")
			}
		})
	}
}

// TestScenarioSetIsWellFormed checks the stimulus itself: every scenario
// names a real world, ids are unique, all three volatility classes are
// represented, and neither the scaffold nor any prompt tells the agent how
// to word what it records.
func TestScenarioSetIsWellFormed(t *testing.T) {
	scenarios := Scenarios()
	seen := map[string]bool{}
	classes := map[string]int{}
	for _, sc := range scenarios {
		if seen[sc.ID] {
			t.Errorf("duplicate scenario id %s", sc.ID)
		}
		seen[sc.ID] = true
		classes[sc.Class]++
		if _, ok := apigen.WorldByName(sc.World); !ok {
			t.Errorf("%s names world %q, which is not in the registry", sc.ID, sc.World)
		}
		if sc.Budget <= 0 {
			t.Errorf("%s has no tool-call budget", sc.ID)
		}
		if sc.Reaches == "" {
			t.Errorf("%s does not record what its world makes true", sc.ID)
		}
	}
	for _, class := range []string{ClassPerishable, ClassDurable, ClassEternal} {
		if classes[class] == 0 {
			t.Errorf("no scenario reaches the %s class", class)
		}
	}
	// The scaffold and the prompts must not carry the manipulations the
	// study measures, or the corpus would be evidence of the harness's
	// wording rather than the platform's.
	steering := []string{"as of", "dated", "do not substitute", "re-check", "recheck", "verify", "timestamp"}
	texts := map[string]string{"system scaffold": System}
	for _, sc := range scenarios {
		texts[sc.ID+" prompt"] = sc.Prompt
	}
	for where, text := range texts {
		lower := strings.ToLower(text)
		for _, s := range steering {
			if strings.Contains(lower, s) {
				t.Errorf("%s contains %q, which steers the phrasing under study", where, s)
			}
		}
	}
	if err := ValidateScenarios(); err != nil {
		t.Errorf("scenario set does not validate: %v", err)
	}
}

// TestArchiveFailureIsReported checks an unwritable output directory fails
// the run loudly: a corpus that ran but was not archived is a wasted run,
// and the one thing worse than losing it is not knowing it was lost.
func TestArchiveFailureIsReported(t *testing.T) {
	opts, _ := newRun(t, "text")
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts.OutDir = filepath.Join(blocked, "corpus")
	corpus, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("run reported success with no archive written")
	}
	if corpus == nil || len(corpus.Episodes) == 0 {
		t.Error("the corpus was discarded along with the archive error")
	}
}

// TestCaptureWaitEndsEmpty checks that an episode which records nothing is
// archived as an episode with no captured insight, not as a failure: a
// model declining to record is itself corpus evidence.
func TestCaptureWaitEndsEmpty(t *testing.T) {
	opts, _ := newRun(t, "text")
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":0,"data":[]}`))
	}))
	t.Cleanup(empty.Close)
	opts.Insights = lifecycleapi.New(empty.URL, empty.Client())
	opts.CaptureWait = 50 * time.Millisecond
	corpus, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, ep := range corpus.Episodes {
		if ep.Error != "" {
			t.Errorf("%s: capturing nothing was reported as an error: %s", ep.ScenarioID, ep.Error)
		}
		if len(ep.Captured) != 0 {
			t.Errorf("%s: captured %d insights against an empty store", ep.ScenarioID, len(ep.Captured))
		}
	}
	if corpus.Manifest.CapturedTotal != 0 {
		t.Errorf("captured total = %d, want 0", corpus.Manifest.CapturedTotal)
	}
}

// TestCaptureReadFailureIsRecorded checks that losing the read-back is
// recorded on the episode rather than silently archived as "captured
// nothing", which would be indistinguishable from a model that declined.
func TestCaptureReadFailureIsRecorded(t *testing.T) {
	opts, _ := newRun(t, "text")
	// Preflight succeeds against a clean store; the episode read-back is
	// what breaks, which is the loss this test is about.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total":0,"data":[]}`))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	opts.Insights = lifecycleapi.New(broken.URL, broken.Client())
	corpus, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, ep := range corpus.Episodes {
		if !strings.Contains(ep.Error, "read captured") {
			t.Errorf("%s: read-back failure not recorded (error %q)", ep.ScenarioID, ep.Error)
		}
	}
}

// TestPreflightRefusesContaminatedIdentities checks the run refuses when a
// pool identity already holds knowledge. An agent that finds its own
// earlier insight declines to record it again, so the episode would be
// archived as "capture wrote nothing" when the truth is "the knowledge was
// already there" — the two are indistinguishable in the archive and mean
// opposite things.
func TestPreflightRefusesContaminatedIdentities(t *testing.T) {
	opts, runner := newRun(t, "an insight this identity recorded in an earlier run")
	dirty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"data":[{"id":"old","captured_by":"bench-agent-001@apikey.local","insight_text":"left over"}]}`))
	}))
	t.Cleanup(dirty.Close)
	opts.Insights = lifecycleapi.New(dirty.URL, dirty.Client())
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("run started against identities that already hold knowledge")
	}
	if !strings.Contains(err.Error(), "already holds") {
		t.Errorf("error %q does not name the contamination", err)
	}
	if runner.calls != 0 {
		t.Errorf("preflight spent %d episodes before refusing", runner.calls)
	}
}
