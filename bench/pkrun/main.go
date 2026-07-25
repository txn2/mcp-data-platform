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
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

func main() {
	var (
		url         = flag.String("url", "http://localhost:8098", "platform MCP + admin REST base URL")
		credential  = flag.String("credential", "", "admin API key (Bearer)")
		fixtureURL  = flag.String("fixture-url", "http://127.0.0.1:8112", "perishable fixture control-plane base URL")
		fixtureKey  = flag.String("fixture-key", "", "fixture X-API-Key")
		model       = flag.String("model", "sonnet", "claude-cli model alias or id")
		cellSet     = flag.String("cells", "prerun", "cell set: prerun, costsweep, answersweep")
		k           = flag.Int("k", 8, "replicates per cell")
		identityKey = flag.Int("identity-keys", 150, "configured identity pool size")
		out         = flag.String("out", "", "output directory (required)")
		gitCommit   = flag.String("git-commit", "", "git commit recorded in the manifest")
	)
	flag.Parse()
	if err := run(*url, *credential, *fixtureURL, *fixtureKey, *model, *cellSet, *out, *gitCommit, *k, *identityKey); err != nil {
		fmt.Fprintln(os.Stderr, "pkrun:", err)
		os.Exit(1)
	}
}

// run wires the clients and executes the cell set.
func run(url, credential, fixtureURL, fixtureKey, model, cellSet, out, gitCommit string, k, identityKeys int) error {
	if out == "" {
		return errors.New("-out is required")
	}
	cells, exploratory, err := selectCells(cellSet)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: url, Credential: credential}
	runner, err := claudecli.New(claudecli.Options{Model: model})
	if err != nil {
		return err
	}
	ctx := context.Background()
	version, err := runner.Version(ctx)
	if err != nil {
		return fmt.Errorf("claude --version: %w", err)
	}
	insights := lifecycleapi.New(url, tgt.HTTPClient(30*time.Second))
	res, runErr := pkrun.Run(ctx, pkrun.Options{
		Target:        tgt,
		IdentityKeys:  identityKeys,
		Fixture:       fixturectl.New(fixtureURL, fixtureKey, 30*time.Second),
		Planter:       pkplant.New(tgt, identityKeys, insights, 30*time.Second),
		Insights:      insights,
		Runner:        runner,
		Cells:         cells,
		K:             k,
		OutDir:        out,
		GitCommit:     gitCommit,
		ClientVersion: version,
		Log:           slog.Default(),
	})
	if res != nil {
		res.Manifest.Exploratory = exploratory
		summarize(res, out)
	}
	return runErr
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
	default:
		return nil, false, fmt.Errorf("unknown cell set %q", name)
	}
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
