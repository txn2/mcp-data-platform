// Command pkrun executes perishable-knowledge study cells against a running
// pk stack (issue #1054). It drives `claude -p`, so a run carries no
// metered cost.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pkcell"
	"github.com/txn2/mcp-data-platform/bench/internal/pkplant"
	"github.com/txn2/mcp-data-platform/bench/internal/pkrun"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
	"github.com/txn2/mcp-data-platform/bench/internal/pollutionplant"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

func main() {
	var (
		url         = flag.String("url", "http://localhost:8098", "platform MCP + admin REST base URL")
		credential  = flag.String("credential", "", "admin API key (Bearer)")
		fixtureURL  = flag.String("fixture-url", "http://127.0.0.1:8112", "perishable fixture control-plane base URL")
		fixtureKey  = flag.String("fixture-key", "", "fixture X-API-Key")
		model       = flag.String("model", "sonnet", "model alias or id (claude-cli alias, or full id with -llm anthropic)")
		llmKind     = flag.String("llm", "claude-cli", "episode driver: claude-cli (subscription, default) or anthropic (raw API, metered)")
		cellSet     = flag.String("cells", "prerun", "cell set: prerun, costsweep, answersweep, bridge, bridge-directive, pollution-coverage, pollution-monitor-count, staleanswer")
		scaffold    = flag.String("scaffold", "default", "episode system prompt: default, or no-discovery (gate probe: drops the harness's own search bullet)")
		k           = flag.Int("k", 8, "replicates per cell")
		identityKey = flag.Int("identity-keys", 150, "configured identity pool size")
		out         = flag.String("out", "", "output directory (required)")
		gitCommit   = flag.String("git-commit", "", "git commit recorded in the manifest")
		disallow    = flag.String("disallow-tools", "", "comma-separated client tools to forbid IN ADDITION to the built-in disallow list (-llm claude-cli); the effective list is recorded on the manifest")
	)
	flag.Parse()
	cfg := runConfig{
		url: *url, credential: *credential, fixtureURL: *fixtureURL, fixtureKey: *fixtureKey,
		model: *model, cellSet: *cellSet, scaffold: *scaffold, out: *out, gitCommit: *gitCommit,
		llmKind: *llmKind, disallowTools: *disallow, k: *k, identityKeys: *identityKey,
	}
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "pkrun:", err)
		os.Exit(1)
	}
}

// runConfig is the parsed command line.
type runConfig struct {
	url           string
	credential    string
	fixtureURL    string
	fixtureKey    string
	model         string
	cellSet       string
	scaffold      string
	out           string
	gitCommit     string
	llmKind       string
	disallowTools string
	k             int
	identityKeys  int
}

// run wires the clients and executes the cell set.
func run(cfg runConfig) error {
	if cfg.out == "" {
		return errors.New("-out is required")
	}
	cells, exploratory, err := selectCells(cfg.cellSet)
	if err != nil {
		return err
	}
	scaffoldText, err := selectScaffold(cfg.scaffold)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	ctx := context.Background()
	runner, version, disallowed, err := buildRunner(ctx, cfg.llmKind, cfg.model, cfg.disallowTools)
	if err != nil {
		return err
	}
	insights := lifecycleapi.New(cfg.url, tgt.HTTPClient(30*time.Second))
	res, runErr := pkrun.Run(ctx, pkrun.Options{
		Target:          tgt,
		IdentityKeys:    cfg.identityKeys,
		Fixture:         fixturectl.New(cfg.fixtureURL, cfg.fixtureKey, 30*time.Second),
		Planter:         pkplant.New(tgt, cfg.identityKeys, insights, 30*time.Second),
		Insights:        insights,
		Runner:          runner,
		Cells:           cells,
		Scaffold:        scaffoldText,
		K:               cfg.k,
		OutDir:          cfg.out,
		GitCommit:       cfg.gitCommit,
		ClientVersion:   version,
		DisallowedTools: disallowed,
		Log:             slog.Default(),
	})
	if res != nil {
		res.Manifest.Exploratory = exploratory
		summarize(res, cfg.out)
	}
	return runErr
}

// buildRunner constructs the episode driver. The claude-cli path records the
// client version and the effective tool-disallow list in the manifest; the
// raw-API path has neither a client to version nor a client tool surface to
// close, which is the point of running it.
func buildRunner(ctx context.Context, llmKind, model, disallowTools string) (pkrun.EpisodeRunner, string, []string, error) {
	switch llmKind {
	case "claude-cli":
		disallowed, err := claudecli.DisallowTools(disallowTools)
		if err != nil {
			return nil, "", nil, err
		}
		runner, err := claudecli.New(claudecli.Options{Model: model, DisallowedTools: disallowed})
		if err != nil {
			return nil, "", nil, err
		}
		version, err := runner.Version(ctx)
		if err != nil {
			return nil, "", nil, fmt.Errorf("claude --version: %w", err)
		}
		return runner, version, runner.DisallowedTools(), nil
	case "anthropic":
		if disallowTools != "" {
			return nil, "", nil, errors.New("-disallow-tools applies to -llm claude-cli only: the raw-API driver exposes no client tools")
		}
		runner, err := pkrun.NewLoopRunner(model, 120*time.Second)
		return runner, "", nil, err
	default:
		return nil, "", nil, fmt.Errorf("unknown -llm %q", llmKind)
	}
}

// selectScaffold resolves the named scaffold variant to its text.
func selectScaffold(name string) (string, error) {
	switch name {
	case "default":
		return pkrun.System, nil
	case "no-discovery":
		return pkrun.SystemNoDiscovery, nil
	default:
		return "", fmt.Errorf("unknown -scaffold %q", name)
	}
}

// selectCells resolves the named cell set and whether it is exploratory.
func selectCells(name string) ([]pkcell.Cell, bool, error) {
	switch name {
	case "prerun":
		cells, err := pkcell.PreRunCells()
		return cells, true, err
	case "costsweep":
		cells, err := pkcell.CostSweepCells()
		return cells, true, err
	case "answersweep":
		cells, err := pkcell.AnswerSweepCells()
		return cells, true, err
	case "bridge":
		cells, err := pkcell.BridgeProbeCells()
		return cells, true, err
	case "bridge-directive":
		cells, err := pkcell.BridgeDirectiveProbeCells()
		return cells, true, err
	case "pollution-coverage":
		// The knowledge-pollution study's cross-fixture unit (#1163): the
		// convention question with no per-episode belief, on a stack whose
		// shared store carries the convention. Confirmatory for that study,
		// so it is not marked exploratory; its adoption classification is
		// applied to the archived answers afterwards.
		cells, err := pkcell.StoreDeliveredCells(
			pollutionplant.QuestionCoverageDays, pollutionplant.CoverageWorld())
		return cells, false, err
	case "pollution-monitor-count":
		cells, err := monitorCountCells()
		return cells, false, err
	case "staleanswer":
		cells, err := pkcell.StaleAnswerCells()
		return cells, true, err
	default:
		return nil, false, fmt.Errorf("unknown cell set %q", name)
	}
}

// monitorCountCells builds the knowledge-pollution study's cross-fixture
// CHECKABLE unit (protocol 6.5).
//
// Unlike the coverage cell it needs no delivered belief: the question is
// answerable from the world by one listing call, which is what makes it the
// checkable class and the analog of the warehouse order count. A plain
// no-seed cell is therefore the right construction and Derive already yields
// it; the behavior is asserted rather than assumed, because a cell that
// derived anything but "answer" would grade every episode against a refusal
// the fixture does not warrant.
func monitorCountCells() ([]pkcell.Cell, error) {
	q, err := pkcell.QuestionByID(pollutionplant.QuestionMonitorCount)
	if err != nil {
		return nil, err
	}
	c, err := pkcell.Derive(q, nil, pkseed.Metadata{}, pollutionplant.CoverageWorldName)
	if err != nil {
		return nil, err
	}
	if c.Behavior != pkcell.BehaviorAnswer {
		return nil, fmt.Errorf("the monitor-count cell derives %s; the checkable cross-fixture unit must be answerable from the world", c.Behavior)
	}
	return []pkcell.Cell{c}, nil
}

// tally counts one cell's outcomes.
type tally struct{ n, verified, correct, trusted, failed int }

// summarize prints the rates the pre-run exists to estimate.
func summarize(res *pkrun.Results, out string) {
	byCell := map[string]*tally{}
	for _, a := range res.Attempts {
		t, ok := byCell[a.CellID]
		if !ok {
			t = &tally{}
			byCell[a.CellID] = t
		}
		if a.Error != "" {
			t.failed++
			continue
		}
		t.n++
		if a.Outcome.Observation.Verified {
			t.verified++
		}
		if a.Outcome.Correct != nil && *a.Outcome.Correct {
			t.correct++
		}
		if a.Trusted {
			t.trusted++
		}
	}
	fmt.Printf("\n%-56s %4s %9s %8s %8s %7s\n", "cell", "n", "verified", "correct", "trusted", "failed")
	for _, c := range res.Cells {
		if t, ok := byCell[c.ID]; ok {
			fmt.Printf("%-56s %4d %9d %8d %8d %7d\n", c.ID, t.n, t.verified, t.correct, t.trusted, t.failed)
		}
	}
	fmt.Printf("\narchive: %s\n", out)
}
