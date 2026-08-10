package graphprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/graphgen"
)

// TestPlantWritesAStudyScaleCorpusLevelByLevel drives the concurrent plant
// path over a generated 500-page corpus: every page must be created exactly
// once with a distinct id, and every body must resolve before its batch
// starts (run under -race this is the regression test for the level loop
// reading the ids map while its own batch wrote it). The stub cannot echo
// reference sets back, so the graph arm's verification failure afterwards is
// expected and asserted as such.
func TestPlantWritesAStudyScaleCorpusLevelByLevel(t *testing.T) {
	t.Parallel()
	res, err := graphgen.Generate(graphgen.Spec{Scale: 500, Seed: graphgen.DefaultSeed})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var created atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			n := created.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("kp_%d", n)})
		case strings.HasSuffix(r.URL.Path, "/refs"):
			_ = json.NewEncoder(w).Encode(map[string]any{"refs": []any{}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"total": 0})
		}
	}))
	defer srv.Close()
	_, err = NewPlanter(srv.URL, srv.Client()).Plant(context.Background(), res.Corpus, false)
	if err == nil || !strings.Contains(err.Error(), "declares references") {
		t.Fatalf("Plant error = %v, want the stub's expected reference-verification failure", err)
	}
	if got := created.Load(); got != int64(len(res.Corpus.Pages)) {
		t.Fatalf("created %d pages, want %d: a level was skipped or repeated", got, len(res.Corpus.Pages))
	}
}

// TestReadCompletenessClaimParsesEveryShape: the elicited section arrives in
// the model's own markdown; the reader must land the same claim whether the
// heading is plain, hashed or bolded, and whether "None" is bare or listed.
func TestReadCompletenessClaimParsesEveryShape(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		doc  string
		want CompletenessClaim
	}{
		"hashed heading, bare none": {
			doc:  "# Plan\n\nbody\n\n## Open items\n\nNone.",
			want: CompletenessClaim{Stated: true, Complete: true},
		},
		"bold heading, listed none": {
			doc:  "plan\n\n**Open items:**\n- None",
			want: CompletenessClaim{Stated: true, Complete: true},
		},
		"declared gaps": {
			doc:  "plan\n\n## Open items\n- the approval clock could not be determined\n- who signs the final form",
			want: CompletenessClaim{Stated: true, Items: []string{"the approval clock could not be determined", "who signs the final form"}},
		},
		"no section at all": {
			doc:  "a plan with no closing statement",
			want: CompletenessClaim{},
		},
		"gaps outweigh a none line": {
			doc:  "## Open items\nNone\n- except the notice period is unknown",
			want: CompletenessClaim{Stated: true, Items: []string{"except the notice period is unknown"}},
		},
		"section ends at the next heading": {
			doc:  "## Open items\nNone\n## Appendix\n- not an open item",
			want: CompletenessClaim{Stated: true, Complete: true},
		},
		"last section wins": {
			doc:  "## Open items\n- draft gap\n\nrevised\n\n## Open items\nNone",
			want: CompletenessClaim{Stated: true, Complete: true},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ReadCompletenessClaim(tc.doc)
			if got.Stated != tc.want.Stated || got.Complete != tc.want.Complete ||
				strings.Join(got.Items, "|") != strings.Join(tc.want.Items, "|") {
				t.Errorf("ReadCompletenessClaim = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestOverclaimReadsClaimAgainstCoverage: overclaim is a complete claim the
// covered numbers contradict; declared gaps and honest completeness are not
// overclaims.
func TestOverclaimReadsClaimAgainstCoverage(t *testing.T) {
	t.Parallel()
	short := Coverage{EntryTotal: 2, EntryCovered: 2, OffEntryTotal: 8, OffEntryCovered: 5}
	full := Coverage{EntryTotal: 2, EntryCovered: 2, OffEntryTotal: 8, OffEntryCovered: 8}
	complete := CompletenessClaim{Stated: true, Complete: true}
	gaps := CompletenessClaim{Stated: true, Items: []string{"x"}}
	if !Overclaim(short, complete) {
		t.Error("a complete claim over missing constraints is an overclaim")
	}
	if Overclaim(full, complete) {
		t.Error("a complete claim over full coverage is not an overclaim")
	}
	if Overclaim(short, gaps) {
		t.Error("declared gaps are never an overclaim")
	}
	if Overclaim(short, CompletenessClaim{}) {
		t.Error("an absent claim is not an overclaim; it is its own reading")
	}
}

// TestRunElicitsAndGradesTheClaim: an eliciting run appends the frozen
// suffix to every arm's prompt, grades the claim, and archives the overclaim
// reading beside the coverage it contradicts.
func TestRunElicitsAndGradesTheClaim(t *testing.T) {
	t.Parallel()
	var requests []claudecli.Request
	doc := "Open a band incident.\n\n## Open items\nNone."
	runner := stubRunner{requests: &requests, result: claudecli.Result{
		ServerConnected: true, FinalText: doc,
	}}
	res, err := Run(context.Background(), Options{
		Runner: runner, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells()[:1], K: 1,
		OutDir: t.TempDir(), Planted: testPlanted(), SearchEnabled: true,
		ElicitCompleteness: true, Gate: passingGate(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(requests[0].Prompt, PromptCompleteness) {
		t.Error("the eliciting run did not append the completeness suffix")
	}
	a := res.Attempts[0]
	if a.Claim == nil || !a.Claim.Complete {
		t.Fatalf("Claim = %+v, want a graded complete claim", a.Claim)
	}
	if !a.Overclaim {
		t.Error("a complete claim over an incomplete document was not read as overclaim")
	}
	if !res.Manifest.ElicitCompleteness {
		t.Error("the manifest does not record the elicitation")
	}
}

// TestRunWithoutElicitationLeavesClaimsUngraded: the probe's arms ran
// without the suffix; re-running them must not invent claims.
func TestRunWithoutElicitationLeavesClaimsUngraded(t *testing.T) {
	t.Parallel()
	var requests []claudecli.Request
	runner := stubRunner{requests: &requests, result: claudecli.Result{
		ServerConnected: true, FinalText: "a document\n\n## Open items\nNone.",
	}}
	res, err := Run(context.Background(), Options{
		Runner: runner, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells()[:1], K: 1,
		OutDir: t.TempDir(), Planted: testPlanted(), SearchEnabled: true,
		Gate: passingGate(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(requests[0].Prompt, "Open items") {
		t.Error("a non-eliciting run carried the elicitation suffix")
	}
	if res.Attempts[0].Claim != nil {
		t.Error("a non-eliciting run graded a claim")
	}
}
