// Command graphprobe drives the graph-completion premise probe (#1241)
// against a running gt stack: it prints the fixture, plants it through the
// platform's own knowledge-page API in either corpus arm (graph or stripped),
// runs the pre-stated sweep gate over `search`, and executes the completion
// cells through `claude -p` (so a run carries no metered cost) under either
// search condition.
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
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/graphprobe"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "graphprobe:", err)
		os.Exit(1)
	}
}

// searchToolFullName is the search tool as the client namespaces it
// (mcp__<server>__<tool> with claudecli's default server name); the no-search
// arms pass it to --disallowedTools.
const searchToolFullName = "mcp__bench__search"

// config is the parsed command line.
type config struct {
	mode         string
	url          string
	credential   string
	plantPath    string
	gatePath     string
	out          string
	model        string
	gitCommit    string
	disallow     string
	strip        bool
	noSearch     bool
	k            int
	identityKeys int
}

// parseFlags reads the command line.
func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "table", "table (print the fixture), plant, gate, run, reread, or reset")
	flag.StringVar(&cfg.url, "url", "http://localhost:8098", "platform MCP + admin REST base URL")
	flag.StringVar(&cfg.credential, "credential", "", "admin API key (Bearer)")
	flag.StringVar(&cfg.plantPath, "plant", "build/bench-results/graph-completion-plant.json", "path the plant record is written to and read from")
	flag.StringVar(&cfg.gatePath, "gate", "build/bench-results/graph-completion-gate.json", "path the sweep-gate report is written to and read from")
	flag.StringVar(&cfg.out, "out", "", "run output directory (required for -mode run)")
	flag.StringVar(&cfg.model, "model", "sonnet", "claude-cli model alias")
	flag.StringVar(&cfg.gitCommit, "git-commit", "", "git commit recorded in the manifest")
	flag.StringVar(&cfg.disallow, "disallow-tools", "", "comma-separated client tools to forbid in addition to the built-in disallow list")
	flag.BoolVar(&cfg.strip, "strip", false, "plant the stripped arm: prose fallbacks instead of reference tokens")
	flag.BoolVar(&cfg.noSearch, "no-search", false, "run the no-search condition: the search tool is disallowed and each prompt opens with the cell's entry reference")
	flag.IntVar(&cfg.k, "k", 3, "replicates per cell")
	flag.IntVar(&cfg.identityKeys, "identity-keys", 64, "configured identity pool size")
	flag.Parse()
	return cfg
}

// httpTimeout bounds every REST and MCP call the probe makes itself. Episodes
// are bounded by the client, not by this.
const httpTimeout = 60 * time.Second

// run dispatches the mode.
func run(cfg config) error {
	ctx := context.Background()
	switch cfg.mode {
	case "table":
		return printTable()
	case "plant":
		return plant(ctx, cfg)
	case "gate":
		return gate(ctx, cfg)
	case "run":
		return episodes(ctx, cfg)
	case "reset":
		return reset(ctx, cfg)
	case "reread":
		return reread(cfg)
	default:
		return fmt.Errorf("unknown -mode %q", cfg.mode)
	}
}

// printTable renders the fixture without touching a platform: each cell, its
// constraint set with every source page's reference distance from the entry,
// and the corpus around them.
func printTable() error {
	if err := graphfix.Validate(); err != nil {
		return err
	}
	for _, c := range graphfix.CompletionCells() {
		depths := c.Depths()
		fmt.Printf("%s  (entry %s, %d constraints, %d off-entry)\n",
			c.ID, c.EntryKey, len(c.Constraints), len(c.OffEntry()))
		for _, k := range c.Constraints {
			kind := "off-entry"
			if c.Entry(k) {
				kind = "entry"
			}
			fmt.Printf("  %-16s %-9s %s\n", k.ID, kind, k.Desc)
			for _, key := range k.Pages {
				d := "unreachable"
				if v, ok := depths[key]; ok {
					d = fmt.Sprintf("d%d", v)
				}
				fmt.Printf("  %-16s %-9s   %s (%s)\n", "", "", key, d)
			}
		}
		fmt.Println()
	}
	pages := graphfix.Pages()
	fmt.Printf("%d pages, %d cells\n\n", len(pages), len(graphfix.CompletionCells()))
	fmt.Printf("%-32s %5s  %s\n", "page", "refs", "title")
	for _, p := range pages {
		fmt.Printf("%-32s %5d  %s\n", p.Key, len(p.Refs()), p.Title)
	}
	return nil
}

// plant writes the corpus in the requested arm and records the ids.
func plant(ctx context.Context, cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential is required to plant")
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	planted, err := graphprobe.NewPlanter(cfg.url, tgt.HTTPClient(httpTimeout)).Plant(ctx, cfg.strip)
	if err != nil {
		return err
	}
	if err := writeJSON(cfg.plantPath, planted); err != nil {
		return err
	}
	fmt.Printf("planted %d pages (%s arm); record written to %s\n", len(planted.Pages), planted.Arm(), cfg.plantPath)
	return nil
}

// reset deletes a previously planted corpus so the other arm can be planted.
func reset(ctx context.Context, cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential is required to reset")
	}
	planted, err := readPlanted(cfg.plantPath)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	if err := graphprobe.NewPlanter(cfg.url, tgt.HTTPClient(httpTimeout)).Delete(ctx, planted); err != nil {
		return err
	}
	fmt.Printf("deleted %d pages (%s arm)\n", len(planted.Pages), planted.Arm())
	return nil
}

// gate runs the sweep gate and records it. It exits non-zero when the sweep
// fails, because the gate is the probe's pre-stated precondition rather than
// a diagnostic.
func gate(ctx context.Context, cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential is required to gate")
	}
	planted, err := readPlanted(cfg.plantPath)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	report, err := graphprobe.Gate(ctx, tgt, cfg.identityKeys, planted, httpTimeout)
	if err != nil {
		return err
	}
	if err := writeJSON(cfg.gatePath, report); err != nil {
		return err
	}
	printGate(report)
	fmt.Printf("\ngate report written to %s\n", cfg.gatePath)
	if !report.Pass {
		return errors.New("the sweep gate did not pass; re-author the leaking cell before running episodes")
	}
	return nil
}

// printGate renders the sweep table: one row per (cell, query, limit).
func printGate(report graphprobe.GateReport) {
	fmt.Printf("%-22s %5s %7s %6s %6s  %s\n", "cell", "limit", "entry", "leaks", "pass", "constraint pages surfaced")
	for _, r := range report.Results {
		fmt.Printf("%-22s %5d %7s %6d %6t  %s\n",
			r.CellID, r.Limit, rank(r.EntryRank), len(r.Leaks), r.Pass, rankList(r.PageRanks))
		fmt.Printf("%-22s %5s   query: %s\n", "", "", r.Query)
	}
}

// rank renders a hit position for the gate table.
func rank(at int) string {
	if at == 0 {
		return "-"
	}
	return fmt.Sprintf("#%d", at)
}

// rankList renders the constraint pages a sweep query surfaced.
func rankList(ranks map[string]int) string {
	if len(ranks) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(ranks))
	for k := range ranks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return ranks[keys[i]] < ranks[keys[j]] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s#%d", k, ranks[k]))
	}
	return strings.Join(parts, " ")
}

// episodes runs the completion cells under one (arm, search) condition.
func episodes(ctx context.Context, cfg config) error {
	switch {
	case cfg.credential == "":
		return errors.New("-credential is required to run")
	case cfg.out == "":
		return errors.New("-out is required to run")
	}
	planted, err := readPlanted(cfg.plantPath)
	if err != nil {
		return err
	}
	var report graphprobe.GateReport
	if err := readJSON(cfg.gatePath, &report); err != nil {
		return fmt.Errorf("reading the sweep-gate report: %w", err)
	}
	runner, version, err := buildRunner(ctx, cfg)
	if err != nil {
		return err
	}
	res, runErr := graphprobe.Run(ctx, graphprobe.Options{
		Target:          target.Target{BaseURL: cfg.url, Credential: cfg.credential},
		IdentityKeys:    cfg.identityKeys,
		Runner:          runner,
		Planted:         planted,
		Gate:            report,
		Cells:           graphfix.CompletionCells(),
		SearchEnabled:   !cfg.noSearch,
		K:               cfg.k,
		OutDir:          cfg.out,
		GitCommit:       cfg.gitCommit,
		ClientVersion:   version,
		DisallowedTools: runner.DisallowedTools(),
		Log:             slog.Default(),
	})
	if res != nil {
		summarize(res, cfg.out)
	}
	return runErr
}

// buildRunner assembles the claude-cli runner for this run's search
// condition: the no-search arms disallow the platform's search tool at the
// client, which is the whole manipulation (the platform is unchanged, and
// `fetch` is not behind the search-first gate).
func buildRunner(ctx context.Context, cfg config) (*claudecli.Runner, string, error) {
	extra := cfg.disallow
	if cfg.noSearch {
		if extra == "" {
			extra = searchToolFullName
		} else {
			extra += "," + searchToolFullName
		}
	}
	disallowed, err := claudecli.DisallowTools(extra)
	if err != nil {
		return nil, "", err
	}
	runner, err := claudecli.New(claudecli.Options{Model: cfg.model, DisallowedTools: disallowed})
	if err != nil {
		return nil, "", err
	}
	version, err := runner.Version(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("claude --version: %w", err)
	}
	return runner, version, nil
}

// tally accumulates one cell's episode outcomes.
type tally struct {
	n, failed                  int
	offCovered, offGrounded    int
	offTotal, unread           int
	entryCovered, entryTotal   int
	searches, fetches          int
	maxDepthRead, maxTravDepth int
}

// add folds one attempt into its cell's tally.
func (t *tally) add(a graphprobe.CompletionAttempt) {
	if a.Error != "" {
		t.failed++
		return
	}
	t.n++
	t.offCovered += a.Coverage.OffEntryCovered
	t.offGrounded += a.Coverage.OffEntryGrounded
	t.offTotal += a.Coverage.OffEntryTotal
	t.unread += a.Coverage.UnreadCovered
	t.entryCovered += a.Coverage.EntryCovered
	t.entryTotal += a.Coverage.EntryTotal
	t.searches += len(a.Reading.Searches)
	t.fetches += len(a.Reading.Fetches)
	if a.Reading.MaxDepthRead > t.maxDepthRead {
		t.maxDepthRead = a.Reading.MaxDepthRead
	}
	if a.Reading.MaxTraversalDepth > t.maxTravDepth {
		t.maxTravDepth = a.Reading.MaxTraversalDepth
	}
}

// coverageRatio renders covered/total as a fraction.
func coverageRatio(covered, total int) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", float64(covered)/float64(total))
}

// summarize prints the per-cell rates the kill conditions are read from.
func summarize(res *graphprobe.CompletionResults, out string) {
	byCell := map[string]*tally{}
	for i := range res.Attempts {
		a := res.Attempts[i]
		t, ok := byCell[a.CellID]
		if !ok {
			t = &tally{maxDepthRead: -1, maxTravDepth: -1}
			byCell[a.CellID] = t
		}
		t.add(a)
	}
	fmt.Printf("\narm=%s search=%t model=%s\n", res.Manifest.Arm, res.Manifest.SearchEnabled, res.Manifest.Model)
	fmt.Printf("%-22s %3s %9s %9s %7s %7s %9s %9s %9s %8s %7s\n",
		"cell", "n", "off-cov", "off-grnd", "unread", "entry", "searches", "fetches", "max-read", "max-trav", "failed")
	for _, c := range res.Cells {
		t, ok := byCell[c.ID]
		if !ok {
			continue
		}
		fmt.Printf("%-22s %3d %9s %9s %7d %7s %9d %9d %9d %8d %7d\n",
			c.ID, t.n,
			coverageRatio(t.offCovered, t.offTotal), coverageRatio(t.offGrounded, t.offTotal),
			t.unread, coverageRatio(t.entryCovered, t.entryTotal),
			t.searches, t.fetches, t.maxDepthRead, t.maxTravDepth, t.failed)
	}
	fmt.Printf("\narchive: %s\n", out)
}

// reread recomputes an archived run's readings from its transcripts, offline,
// dispatching on the archive's instrument.
func reread(cfg config) error {
	if cfg.out == "" {
		return errors.New("-out is required to reread (the archive directory)")
	}
	probe, err := graphprobe.ArchiveProbe(cfg.out)
	if err != nil {
		return err
	}
	if probe == "" {
		return rereadLookup(cfg)
	}
	res, err := graphprobe.RereadCompletion(cfg.out)
	if err != nil {
		return err
	}
	summarize(res, cfg.out)
	return nil
}

// lookupRow accumulates one lookup-era cell's reread readings.
type lookupRow struct {
	n, correct, fetched, readAnswer, failed int
	maxDepth, maxTraversal, depth           int
}

// add folds one reread attempt into the row.
func (r *lookupRow) add(a graphprobe.LookupAttempt) {
	if a.Error != "" {
		r.failed++
		return
	}
	r.n++
	if a.Outcome.Correct {
		r.correct++
	}
	if a.Reading.FetchedAnyReference {
		r.fetched++
	}
	if a.Reading.ReadAnswerPage {
		r.readAnswer++
	}
	if a.Reading.MaxDepthRead > r.maxDepth {
		r.maxDepth = a.Reading.MaxDepthRead
	}
	if a.Reading.MaxTraversalDepth > r.maxTraversal {
		r.maxTraversal = a.Reading.MaxTraversalDepth
	}
}

// rereadLookup re-derives a lookup-era archive's traversal readings. The
// retired instrument's outcomes are not re-graded (the archive carries them);
// what stays reproducible offline is the reading every register claim cites.
func rereadLookup(cfg config) error {
	attempts, err := graphprobe.RereadLookup(cfg.out)
	if err != nil {
		return err
	}
	fmt.Printf("%-20s %5s %4s %8s %8s %11s %9s %8s %7s\n",
		"cell", "depth", "n", "correct", "fetched", "read-answer", "max-read", "max-trav", "failed")
	rows := map[string]*lookupRow{}
	var order []string
	for _, a := range attempts {
		r, ok := rows[a.CellID]
		if !ok {
			r = &lookupRow{maxDepth: -1, maxTraversal: -1, depth: a.Depth}
			rows[a.CellID] = r
			order = append(order, a.CellID)
		}
		r.add(a)
	}
	for _, id := range order {
		r := rows[id]
		fmt.Printf("%-20s %5d %4d %8d %8d %11d %9d %8d %7d\n",
			id, r.depth, r.n, r.correct, r.fetched, r.readAnswer, r.maxDepth, r.maxTraversal, r.failed)
	}
	return nil
}

// readPlanted loads the plant record.
func readPlanted(path string) (graphprobe.Planted, error) {
	var planted graphprobe.Planted
	if err := readJSON(path, &planted); err != nil {
		return planted, fmt.Errorf("reading the plant record: %w", err)
	}
	if len(planted.Pages) == 0 {
		return planted, fmt.Errorf("%s records no planted pages", path)
	}
	return planted, nil
}

// writeJSON writes a record, creating parent directories.
func writeJSON(path string, v any) error {
	if dir := parentDir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
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

// readJSON reads a record.
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) // #nosec G304 -- operator-supplied harness path
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// parentDir returns the directory part of a path, or "" when there is none.
func parentDir(path string) string {
	if i := strings.LastIndex(path, "/"); i > 0 {
		return path[:i]
	}
	return ""
}
