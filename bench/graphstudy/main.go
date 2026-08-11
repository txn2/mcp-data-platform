// Command graphstudy drives the graph-completion study's corpus work and
// confirmatory matrix (#1250, #1251) against a running gt stack: it
// generates the deterministic study corpus at a chosen scale, runs the
// authoring-time embedding certification offline through a local ollama,
// plants the corpus through the platform's knowledge-page API in either
// arm, runs the live sweep gate with the discontinuity requirement on,
// executes the pre-registered completion cells through `claude -p` with the
// completeness elicitation always on, and rereads an archived run offline
// by regenerating its corpus from the manifest's generator spec.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
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
	out          string
	model        string
	gitCommit    string
	disallow     string
	scale        int
	seed         uint64
	density      int
	k            int
	strip        bool
	noSearch     bool
	identityKeys int
}

// parseFlags reads the command line.
func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "table", "table (print corpus shapes), certify, plant, gate, run, reread, or reset")
	flag.StringVar(&cfg.url, "url", "http://localhost:8098", "platform MCP + admin REST base URL")
	flag.StringVar(&cfg.credential, "credential", "", "admin API key (Bearer)")
	flag.StringVar(&cfg.plantPath, "plant", "", "plant record path (default build/bench-results/graph-study-plant-<scale>.json)")
	flag.StringVar(&cfg.gatePath, "gate", "", "sweep-gate report path (default build/bench-results/graph-study-gate-<scale>.json)")
	flag.StringVar(&cfg.certPath, "cert", "", "embedding certification report path (default build/bench-results/graph-study-cert-<scale>.json)")
	flag.StringVar(&cfg.ollamaURL, "ollama", "http://localhost:11434", "ollama base URL for -mode certify")
	flag.StringVar(&cfg.embedModel, "embed-model", "nomic-embed-text", "ollama embedding model, matching the platform's provider")
	flag.StringVar(&cfg.out, "out", "", "run output directory (required for -mode run and reread)")
	flag.StringVar(&cfg.model, "model", "opus", "claude-cli model alias (the design freezes the probe's stronger tier)")
	flag.StringVar(&cfg.gitCommit, "git-commit", "", "git commit recorded in the manifest")
	flag.StringVar(&cfg.disallow, "disallow-tools", "", "comma-separated client tools to forbid in addition to the built-in disallow list")
	flag.IntVar(&cfg.scale, "scale", graphgen.Scales[0], "corpus scale (total pages)")
	flag.Uint64Var(&cfg.seed, "seed", graphgen.DefaultSeed, "generation seed")
	flag.IntVar(&cfg.density, "density", 0, "filler mean out-degree (0 = default)")
	flag.IntVar(&cfg.k, "k", 5, "replicates per cell (the matrix's pre-registered primary k)")
	flag.BoolVar(&cfg.strip, "strip", false, "plant the stripped arm: prose fallbacks instead of reference tokens")
	flag.BoolVar(&cfg.noSearch, "no-search", false, "run the no-search condition: the search tool is disallowed and each prompt opens with the cell's entry reference")
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
	case "run":
		return episodes(ctx, cfg)
	case "reread":
		return reread(cfg)
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

// episodes runs the confirmatory cells against the planted study corpus
// under one (arm, search) condition, with the completeness elicitation
// always on — the matrix as #1251 pre-registers it. The corpus, its arm and
// its scale all come from the plant record; the manifest carries the
// generator spec so the archive rereads offline.
func episodes(ctx context.Context, cfg config) error {
	switch {
	case cfg.credential == "":
		return errors.New("-credential is required to run")
	case cfg.out == "":
		return errors.New("-out is required to run")
	}
	record, res, err := readPlantRecord(cfg)
	if err != nil {
		return err
	}
	var report graphprobe.GateReport
	if err := readJSON(cfg.path(cfg.gatePath, "gate"), &report); err != nil {
		return fmt.Errorf("reading the sweep-gate report: %w", err)
	}
	runner, version, err := graphprobe.BuildRunner(ctx, cfg.model, cfg.disallow, cfg.noSearch)
	if err != nil {
		return err
	}
	results, runErr := graphprobe.Run(ctx, graphprobe.Options{
		Target:             target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		IdentityKeys:       cfg.identityKeys,
		Runner:             runner,
		Planted:            record.Planted,
		Gate:               report,
		Corpus:             res.Corpus,
		Cells:              res.Corpus.Cells,
		SearchEnabled:      !cfg.noSearch,
		ElicitCompleteness: true,
		Spec:               &record.Spec,
		WithinCeiling:      graphgen.WithinCeiling(len(res.Corpus.Pages)),
		K:                  cfg.k,
		OutDir:             cfg.out,
		GitCommit:          cfg.gitCommit,
		ClientVersion:      version,
		DisallowedTools:    runner.DisallowedTools(),
		Log:                slog.Default(),
	})
	if results != nil {
		summarize(results, cfg.out)
	}
	if runErr == nil {
		runErr = resultError(results)
	}
	return runErr
}

// resultError converts a run in which no attempt produced a result into a
// command failure: the archive holds only harness errors, so a driver that
// treated it as success would reset the corpus and report a matrix cell
// that does not exist.
func resultError(res *graphprobe.CompletionResults) error {
	if res == nil || len(res.Attempts) == 0 {
		return errors.New("the run produced no attempts")
	}
	for _, a := range res.Attempts {
		if a.Error == "" {
			return nil
		}
	}
	return fmt.Errorf("every one of the %d attempts failed (first: %s); the archive holds no result", len(res.Attempts), res.Attempts[0].Error)
}

// reread recomputes an archived study run's readings offline, regenerating
// the exact corpus from the generator spec its manifest carries.
func reread(cfg config) error {
	if cfg.out == "" {
		return errors.New("-out is required to reread (the archive directory)")
	}
	probe, err := graphprobe.ArchiveProbe(cfg.out)
	if err != nil {
		return err
	}
	if probe == "" {
		return errors.New("this archive predates the completion instrument; reread it with the graphprobe command")
	}
	res, err := graphprobe.RereadCompletion(cfg.out)
	if err != nil {
		return err
	}
	summarize(res, cfg.out)
	return nil
}

// studyTally accumulates one cell's episode outcomes across replicates.
type studyTally struct {
	n, failed               int
	offCovered, offGrounded int
	offTotal, unread        int
	discGrounded, discTotal int
	complete, overclaim     int
	noStatement             int
	searches, fetches       int
}

// add folds one attempt into its cell's tally.
func (t *studyTally) add(a graphprobe.CompletionAttempt, cell graphfix.CompletionCell) {
	if a.Error != "" {
		t.failed++
		return
	}
	t.n++
	t.offCovered += a.Coverage.OffEntryCovered
	t.offGrounded += a.Coverage.OffEntryGrounded
	t.offTotal += a.Coverage.OffEntryTotal
	t.unread += a.Coverage.UnreadCovered
	t.searches += len(a.Reading.Searches)
	t.fetches += len(a.Reading.Fetches)
	t.addDiscontinuity(a, cell)
	t.addClaim(a)
}

// addDiscontinuity folds the attempt's discontinuity constraint results in.
func (t *studyTally) addDiscontinuity(a graphprobe.CompletionAttempt, cell graphfix.CompletionCell) {
	for _, cr := range a.Coverage.Constraints {
		k, ok := cell.ConstraintByID(cr.ID)
		if !ok || !k.Discontinuity {
			continue
		}
		t.discTotal++
		if cr.Grounded {
			t.discGrounded++
		}
	}
}

// addClaim folds the attempt's graded completeness claim in.
func (t *studyTally) addClaim(a graphprobe.CompletionAttempt) {
	if a.Claim == nil {
		return
	}
	switch {
	case !a.Claim.Stated:
		t.noStatement++
	case a.Claim.Complete:
		t.complete++
	}
	if a.Overclaim {
		t.overclaim++
	}
}

// ratio renders covered/total as a fraction, "-" for an empty denominator.
func ratio(covered, total int) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", float64(covered)/float64(total))
}

// summarize prints the per-cell rates the kill conditions are read from.
func summarize(res *graphprobe.CompletionResults, out string) {
	byCell := map[string]*studyTally{}
	for i := range res.Attempts {
		a := res.Attempts[i]
		cell, ok := cellByID(res.Cells, a.CellID)
		if !ok {
			continue
		}
		t := byCell[a.CellID]
		if t == nil {
			t = &studyTally{}
			byCell[a.CellID] = t
		}
		t.add(a, cell)
	}
	fmt.Printf("\narm=%s search=%t within-ceiling=%t model=%s pages=%d\n",
		res.Manifest.Arm, res.Manifest.SearchEnabled, res.Manifest.WithinCeiling,
		res.Manifest.Model, res.Manifest.CorpusPages)
	fmt.Printf("%-20s %3s %8s %9s %10s %7s %9s %10s %7s %9s %8s %7s\n",
		"cell", "n", "off-cov", "off-grnd", "disc-grnd", "unread", "complete", "overclaim", "nostmt", "searches", "fetches", "failed")
	for _, c := range res.Cells {
		t, ok := byCell[c.ID]
		if !ok {
			continue
		}
		fmt.Printf("%-20s %3d %8s %9s %10s %7d %9d %10d %7d %9d %8d %7d\n",
			c.ID, t.n, ratio(t.offCovered, t.offTotal), ratio(t.offGrounded, t.offTotal),
			ratio(t.discGrounded, t.discTotal), t.unread, t.complete, t.overclaim,
			t.noStatement, t.searches, t.fetches, t.failed)
	}
	fmt.Printf("\narchive: %s\n", out)
}

// cellByID finds one archived cell.
func cellByID(cells []graphfix.CompletionCell, id string) (graphfix.CompletionCell, bool) {
	for _, c := range cells {
		if c.ID == id {
			return c, true
		}
	}
	return graphfix.CompletionCell{}, false
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

// readJSON reads one artifact.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) // #nosec G304 -- operator-supplied artifact path
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
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
