// Command graphstudy drives the graph-completion study's stage-3 corpus
// work (#1250) against a running gt stack: it generates the deterministic
// study corpus at a chosen scale, runs the authoring-time embedding
// certification offline through a local ollama, plants the corpus through
// the platform's knowledge-page API in either arm, and runs the live sweep
// gate with the discontinuity requirement on. The confirmatory episode
// matrix is #1251; this command ends at demonstrated separation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/graphgen"
	"github.com/txn2/mcp-data-platform/bench/internal/graphprobe"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "graphstudy:", err)
		os.Exit(1)
	}
}

// config is the parsed command line.
type config struct {
	mode         string
	url          string
	credential   string
	plantPath    string
	gatePath     string
	certPath     string
	ollamaURL    string
	embedModel   string
	scale        int
	seed         uint64
	density      int
	strip        bool
	identityKeys int
}

// parseFlags reads the command line.
func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "table", "table (print corpus shapes), certify, plant, gate, or reset")
	flag.StringVar(&cfg.url, "url", "http://localhost:8098", "platform MCP + admin REST base URL")
	flag.StringVar(&cfg.credential, "credential", "", "admin API key (Bearer)")
	flag.StringVar(&cfg.plantPath, "plant", "", "plant record path (default build/bench-results/graph-study-plant-<scale>.json)")
	flag.StringVar(&cfg.gatePath, "gate", "", "sweep-gate report path (default build/bench-results/graph-study-gate-<scale>.json)")
	flag.StringVar(&cfg.certPath, "cert", "", "embedding certification report path (default build/bench-results/graph-study-cert-<scale>.json)")
	flag.StringVar(&cfg.ollamaURL, "ollama", "http://localhost:11434", "ollama base URL for -mode certify")
	flag.StringVar(&cfg.embedModel, "embed-model", "nomic-embed-text", "ollama embedding model, matching the platform's provider")
	flag.IntVar(&cfg.scale, "scale", graphgen.Scales[0], "corpus scale (total pages)")
	flag.Uint64Var(&cfg.seed, "seed", graphgen.DefaultSeed, "generation seed")
	flag.IntVar(&cfg.density, "density", 0, "filler mean out-degree (0 = default)")
	flag.BoolVar(&cfg.strip, "strip", false, "plant the stripped arm: prose fallbacks instead of reference tokens")
	flag.IntVar(&cfg.identityKeys, "identity-keys", 64, "configured identity pool size")
	flag.Parse()
	return cfg
}

// httpTimeout bounds every REST and MCP call the study harness makes.
const httpTimeout = 120 * time.Second

// run dispatches one mode.
func run(cfg config) error {
	ctx := context.Background()
	switch cfg.mode {
	case "table":
		return printTable()
	case "certify":
		return certify(ctx, cfg)
	case "plant":
		return plant(ctx, cfg)
	case "gate":
		return gate(ctx, cfg)
	case "reset":
		return reset(ctx, cfg)
	default:
		return fmt.Errorf("unknown -mode %q", cfg.mode)
	}
}

// spec builds the generation spec from the flags.
func (c config) spec() graphgen.Spec {
	return graphgen.Spec{Scale: c.scale, Seed: c.seed, EdgeDensity: c.density}
}

// path fills a default artifact path scoped by scale.
func (c config) path(explicit, kind string) string {
	if explicit != "" {
		return explicit
	}
	return fmt.Sprintf("build/bench-results/graph-study-%s-%d.json", kind, c.scale)
}

// plantRecord is the study's plant artifact: the plant plus the Spec that
// regenerates the exact corpus it planted, so every later stage reads the
// corpus from the record rather than trusting the operator to repeat flags.
type plantRecord struct {
	Spec    graphgen.Spec      `json:"spec"`
	Planted graphprobe.Planted `json:"planted"`
}

// printTable prints each study scale's corpus shape.
func printTable() error {
	for _, scale := range graphgen.Scales {
		res, err := graphgen.Generate(graphgen.Spec{Scale: scale, Seed: graphgen.DefaultSeed})
		if err != nil {
			return err
		}
		fmt.Printf("scale %d: %d pages (%d core), %d mints\n", scale, len(res.Corpus.Pages), res.CorePages, len(res.Mints))
		for _, cell := range res.Corpus.Cells {
			fmt.Printf("  %-20s closure %2d pages, %d constraints (%d off-entry, %d discontinuity: %v)\n",
				cell.ID, len(res.Corpus.Closure(cell)), len(cell.Constraints),
				len(cell.OffEntry()), len(cell.Discontinuities()), cell.DiscontinuityPages())
		}
	}
	return nil
}

// certify runs the authoring-time embedding certification offline.
func certify(ctx context.Context, cfg config) error {
	res, err := graphgen.Generate(cfg.spec())
	if err != nil {
		return err
	}
	emb := &graphgen.OllamaEmbedder{BaseURL: cfg.ollamaURL, Model: cfg.embedModel}
	report, err := graphgen.CertifyDiscontinuity(ctx, emb, res, graphgen.EffectiveTopK(len(res.Corpus.Pages)), graphgen.CertEntryTopK)
	if err != nil {
		return err
	}
	if err := writeJSON(cfg.path(cfg.certPath, "cert"), report); err != nil {
		return err
	}
	printCert(report)
	fmt.Printf("\ncertification report written to %s\n", cfg.path(cfg.certPath, "cert"))
	if !report.Pass {
		if report.HorizonExceedsCorpus {
			return fmt.Errorf("certification unsatisfiable at scale %d: the top-%d horizon covers at least half the corpus (the within-enumeration-ceiling condition)", cfg.scale, report.TopK)
		}
		return errors.New("the embedding certification did not pass; re-author the violating discontinuity before planting")
	}
	return nil
}

// printCert renders the certification summary: one line per phrasing.
func printCert(report *graphgen.CertReport) {
	fmt.Printf("embedding certification, scale %d, model %s, exclusion top-%d, entry top-%d\n\n",
		report.Spec.Scale, report.Model, report.TopK, report.EntryTopK)
	for _, p := range report.Phrasings {
		status := "pass"
		if !p.Pass {
			status = "FAIL"
		}
		kind := "query "
		if p.Prompt {
			kind = "prompt"
		}
		fmt.Printf("%-20s %s %-4s entry rank %3d  disc violations %v\n",
			p.CellID, kind, status, p.EntryRank, p.DiscontinuityViolations)
	}
}

// plant generates the corpus and writes it through the platform.
func plant(ctx context.Context, cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential is required to plant")
	}
	res, err := graphgen.Generate(cfg.spec())
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	planted, err := graphprobe.NewPlanter(cfg.url, tgt.HTTPClient(httpTimeout)).Plant(ctx, res.Corpus, cfg.strip)
	if err != nil {
		if len(planted.Pages) > 0 {
			// A failed plant that already wrote pages must not lose them: the
			// partial record is what -mode reset deletes by.
			partial := cfg.path(cfg.plantPath, "plant") + ".partial"
			if werr := writeJSON(partial, plantRecord{Spec: res.Spec, Planted: planted}); werr == nil {
				fmt.Printf("plant failed after %d page(s); partial record written to %s — reset with -plant %s before replanting\n",
					len(planted.Pages), partial, partial)
			}
		}
		return err
	}
	record := plantRecord{Spec: res.Spec, Planted: planted}
	if err := writeJSON(cfg.path(cfg.plantPath, "plant"), record); err != nil {
		return err
	}
	fmt.Printf("planted %d pages (%s arm, scale %d); record written to %s\n",
		len(planted.Pages), planted.Arm(), cfg.scale, cfg.path(cfg.plantPath, "plant"))
	return nil
}

// gate regenerates the planted corpus from the record and runs the live
// sweep gate, discontinuity requirement included.
func gate(ctx context.Context, cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential is required to gate")
	}
	record, res, err := readPlantRecord(cfg)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	report, err := graphprobe.Gate(ctx, tgt, cfg.identityKeys, res.Corpus, record.Planted, httpTimeout)
	if err != nil {
		return err
	}
	if err := writeJSON(cfg.path(cfg.gatePath, "gate"), report); err != nil {
		return err
	}
	printGate(res, report)
	fmt.Printf("\ngate report written to %s\n", cfg.path(cfg.gatePath, "gate"))
	if !report.Pass {
		return errors.New("the live sweep gate did not pass at this scale")
	}
	return nil
}

// printGate renders the separation summary the sweep demonstrates: entry
// findability, discontinuity absence, and the enumeration profile of the
// adjacent constraint pages per cell.
func printGate(res *graphgen.Result, report graphprobe.GateReport) {
	fmt.Printf("live sweep gate, scale %d, %s arm, limits %v\n\n", res.Spec.Scale, arm(report.Stripped), report.Limits)
	for _, cell := range res.Corpus.Cells {
		printCellGate(res, cell.ID, report)
	}
}

// printCellGate renders one cell's sweep rows and adjacency profile.
func printCellGate(res *graphgen.Result, cellID string, report graphprobe.GateReport) {
	cell, _ := res.Corpus.CellByID(cellID)
	adjacent := adjacentPages(cell)
	seen := map[string]int{}
	for _, r := range report.Results {
		if r.CellID != cellID {
			continue
		}
		status := "pass"
		if !r.Pass {
			status = "FAIL"
		}
		fmt.Printf("%-20s limit %3d %s entry@%d disc %v  %q\n", cellID, r.Limit, status, r.EntryRank, r.DiscontinuityHits, r.Query)
		foldBestRanks(seen, adjacent, r.PageRanks)
	}
	fmt.Printf("%-20s adjacent constraint pages surfaced: %d of %d %v\n\n", cellID, len(seen), len(adjacent), sortedRanks(seen))
}

// adjacentPages returns a cell's non-discontinuity constraint pages.
func adjacentPages(cell graphfix.CompletionCell) map[string]bool {
	discs := map[string]bool{}
	for _, key := range cell.DiscontinuityPages() {
		discs[key] = true
	}
	adjacent := map[string]bool{}
	for _, key := range cell.AllConstraintPages() {
		if !discs[key] {
			adjacent[key] = true
		}
	}
	return adjacent
}

// foldBestRanks keeps each adjacent page's best rank across the sweep.
func foldBestRanks(seen map[string]int, adjacent map[string]bool, ranks map[string]int) {
	for key, rank := range ranks {
		if adjacent[key] && (seen[key] == 0 || rank < seen[key]) {
			seen[key] = rank
		}
	}
}

// sortedRanks renders a page->best-rank map deterministically.
func sortedRanks(ranks map[string]int) []string {
	keys := make([]string, 0, len(ranks))
	for key := range ranks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s@%d", key, ranks[key]))
	}
	return out
}

// arm names a gate report's corpus arm.
func arm(stripped bool) string {
	if stripped {
		return "stripped"
	}
	return "graph"
}

// reset deletes a previously planted study corpus.
func reset(ctx context.Context, cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential is required to reset")
	}
	record, _, err := readPlantRecord(cfg)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	if err := graphprobe.NewPlanter(cfg.url, tgt.HTTPClient(httpTimeout)).Delete(ctx, record.Planted); err != nil {
		return err
	}
	fmt.Printf("deleted %d pages (%s arm, scale %d)\n", len(record.Planted.Pages), record.Planted.Arm(), record.Spec.Scale)
	return nil
}

// readPlantRecord loads the plant record and regenerates its exact corpus.
func readPlantRecord(cfg config) (plantRecord, *graphgen.Result, error) {
	var record plantRecord
	raw, err := os.ReadFile(cfg.path(cfg.plantPath, "plant")) // #nosec G304 -- operator-supplied artifact path
	if err != nil {
		return record, nil, fmt.Errorf("reading plant record: %w", err)
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, nil, fmt.Errorf("decoding plant record: %w", err)
	}
	res, err := graphgen.Generate(record.Spec)
	if err != nil {
		return record, nil, err
	}
	if len(record.Planted.Pages) != len(res.Corpus.Pages) {
		return record, nil, fmt.Errorf("plant record holds %d pages but spec %+v regenerates %d; the record and generator disagree",
			len(record.Planted.Pages), record.Spec, len(res.Corpus.Pages))
	}
	return record, res, nil
}

// writeJSON writes one artifact, creating parents.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(parentDir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", parentDir(path), err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// parentDir returns the directory of a path, "." for a bare name.
func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
