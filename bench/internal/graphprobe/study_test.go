package graphprobe

// Confirmatory-matrix instrument tests (#1251): the within-ceiling gate
// acceptance at the study's smallest scale, the generator spec in the
// manifest, and the offline reread regenerating a study archive's corpus
// from that spec.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/graphgen"
)

// ceilingGate builds the one failure shape the design pre-states for scale
// 50: every cell's entry surfaces for its prompt phrasing at the modal
// limit, nothing leaks, and the only failures are discontinuity source
// pages inside the swept lists.
func ceilingGate(cells []graphfix.CompletionCell) GateReport {
	report := GateReport{Limits: []int{5, 25, 100}}
	for _, cell := range cells {
		report.Results = append(report.Results, GateResult{
			CellID: cell.ID, Query: cell.GateQueries[0], Limit: 25, EntryRank: 1,
			DiscontinuityHits: map[string]int{"some-institutional-page": 17},
		})
	}
	return report
}

// TestRunAcceptsAWithinCeilingGateOnlyForDiscontinuityOnlyFailures: at the
// smallest scale the sweep records discontinuity hits by construction and a
// run there must proceed; any other failure shape stays disqualifying, and
// without the within-ceiling declaration the failed gate stays refused.
func TestRunAcceptsAWithinCeilingGateOnlyForDiscontinuityOnlyFailures(t *testing.T) {
	t.Parallel()
	cells := graphfix.CompletionCells()
	base := func() Options {
		return Options{
			Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: cells, K: 1, OutDir: t.TempDir(),
			Planted: testPlanted(), SearchEnabled: true,
			Gate: ceilingGate(cells), WithinCeiling: true,
		}
	}
	res, err := Run(context.Background(), base())
	if err != nil {
		t.Fatalf("Run refused the within-ceiling reading: %v", err)
	}
	if !res.Manifest.WithinCeiling {
		t.Error("manifest does not record the within-ceiling acceptance")
	}
	opts := base()
	opts.WithinCeiling = false
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "fixture gate") {
		t.Fatalf("Run error = %v, want the failed gate refused without the within-ceiling declaration", err)
	}
	opts = base()
	opts.Gate.Results[0].Leaks = []string{"inc-first-rung"}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run accepted a within-ceiling gate carrying a signature leak")
	}
	opts = base()
	opts.Gate.Results[0].EntryRank = 0
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run accepted a within-ceiling gate whose entry page never surfaced at the modal limit")
	}
}

// TestManifestWithinCeilingStaysFalseOnAPassingGate: the manifest flag means
// "this archive's gate carries the within-ceiling reading", so a certified
// scale run that passed its gate must not carry it even when the caller set
// the option.
func TestManifestWithinCeilingStaysFalseOnAPassingGate(t *testing.T) {
	t.Parallel()
	res, err := Run(context.Background(), Options{
		Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells(), K: 1, OutDir: t.TempDir(),
		Planted: testPlanted(), SearchEnabled: true,
		Gate: passingGate(), WithinCeiling: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Manifest.WithinCeiling {
		t.Error("manifest records a within-ceiling reading its passing gate does not carry")
	}
}

// studyArchive writes one stub study run at scale 50 and returns its
// directory and spec: generated corpus, spec in the manifest, elicitation
// on, every attempt failed (a stub runner never connects) so no transcript
// is required.
func studyArchive(t *testing.T) (string, graphgen.Spec) {
	t.Helper()
	spec := graphgen.Spec{Scale: 50, Seed: graphgen.DefaultSeed}
	gen, err := graphgen.Generate(spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	pages := map[string]string{}
	for _, p := range gen.Corpus.Pages {
		pages[p.Key] = "kp_" + p.Key
	}
	dir := t.TempDir()
	_, err = Run(context.Background(), Options{
		Runner: stubRunner{}, Corpus: gen.Corpus, Cells: gen.Corpus.Cells, K: 1, OutDir: dir,
		Planted: Planted{Pages: pages}, SearchEnabled: true,
		ElicitCompleteness: true, Spec: &spec,
		Gate: GateReport{Results: []GateResult{{CellID: gen.Corpus.Cells[0].ID, Pass: true}}, Pass: true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return dir, spec
}

// TestRereadRegeneratesAStudyCorpusFromTheManifestSpec: a study archive
// plants more pages than the compiled-in fixture holds, so a reread that
// succeeds proves the corpus came from the archived spec, not the fixture.
func TestRereadRegeneratesAStudyCorpusFromTheManifestSpec(t *testing.T) {
	t.Parallel()
	dir, spec := studyArchive(t)
	res, err := RereadCompletion(dir)
	if err != nil {
		t.Fatalf("RereadCompletion: %v", err)
	}
	if res.Manifest.Spec == nil || *res.Manifest.Spec != spec {
		t.Errorf("manifest spec = %+v, want %+v", res.Manifest.Spec, spec)
	}
	if res.Manifest.CorpusPages != 50 {
		t.Errorf("CorpusPages = %d, want the study scale", res.Manifest.CorpusPages)
	}
}

// TestRereadRefusesAnArchiveWhoseCorpusDoesNotMatch: a tampered spec (or a
// generator whose content drifted since the run) regenerates a corpus whose
// fingerprint disagrees with the manifest's, and reread must refuse rather
// than silently regrade over the wrong reference graph.
func TestRereadRefusesAnArchiveWhoseCorpusDoesNotMatch(t *testing.T) {
	t.Parallel()
	dir, _ := studyArchive(t)
	path := filepath.Join(dir, "results.json")
	raw, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	var res CompletionResults
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decoding archive: %v", err)
	}
	res.Manifest.Spec.Scale = 500
	if err := writeTampered(path, res); err != nil {
		t.Fatal(err)
	}
	if _, err := RereadCompletion(dir); err == nil || !strings.Contains(err.Error(), "hashes differently") {
		t.Fatalf("RereadCompletion error = %v, want a fingerprint refusal", err)
	}
	// A drifted generator with the SAME spec is the subtler case: fake it by
	// perturbing only the archived fingerprint.
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("re-decoding archive: %v", err)
	}
	res.Manifest.CorpusFingerprint = "not-the-corpus"
	if err := writeTampered(path, res); err != nil {
		t.Fatal(err)
	}
	if _, err := RereadCompletion(dir); err == nil || !strings.Contains(err.Error(), "hashes differently") {
		t.Fatalf("RereadCompletion error = %v, want a fingerprint refusal", err)
	}
}

// writeTampered re-encodes one archive in place.
func writeTampered(path string, res CompletionResults) error {
	tampered, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return os.WriteFile(path, tampered, 0o600)
}

// TestRunRefusesAGateFromAnotherPlant: the study's cell ids and entry keys
// are identical at every scale, so the gate report must be bound to the
// exact plant it swept or a leftover report from another scale's corpus
// would validate a run it never gated.
func TestRunRefusesAGateFromAnotherPlant(t *testing.T) {
	t.Parallel()
	planted := testPlanted()
	planted.PlantedAt = time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	gate := passingGate()
	gate.PlantedAt = planted.PlantedAt.Add(time.Hour)
	_, err := Run(context.Background(), Options{
		Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells(), K: 1, OutDir: t.TempDir(),
		Planted: planted, SearchEnabled: true, Gate: gate,
	})
	if err == nil || !strings.Contains(err.Error(), "different plant") {
		t.Fatalf("Run error = %v, want a refusal naming the plant mismatch", err)
	}
	gate.PlantedAt = planted.PlantedAt
	if _, err := Run(context.Background(), Options{
		Runner: stubRunner{}, Corpus: graphfix.Default(), Cells: graphfix.CompletionCells(), K: 1, OutDir: t.TempDir(),
		Planted: planted, SearchEnabled: true, Gate: gate,
	}); err != nil {
		t.Fatalf("Run refused a gate bound to its own plant: %v", err)
	}
}
