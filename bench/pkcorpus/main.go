// Command pkcorpus runs the perishable-knowledge study's capture-corpus
// run (issue #1054, protocol section 6 stage 1): real capture episodes
// over the perishable fixture, archived verbatim. It drives `claude -p`,
// so a corpus run carries no metered cost.
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
	"github.com/txn2/mcp-data-platform/bench/internal/pkcorpus"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

func main() {
	var (
		url         = flag.String("url", "http://localhost:8098", "platform MCP + admin REST base URL")
		credential  = flag.String("credential", "", "admin API key (Bearer)")
		fixtureURL  = flag.String("fixture-url", "http://127.0.0.1:8112", "perishable fixture control-plane base URL")
		fixtureKey  = flag.String("fixture-key", "", "fixture X-API-Key")
		model       = flag.String("model", "sonnet", "claude-cli model alias or id")
		replicates  = flag.Int("replicates", 3, "episodes per scenario")
		identityKey = flag.Int("identity-keys", 150, "configured identity pool size")
		out         = flag.String("out", "", "output directory for the archived corpus (required)")
		gitCommit   = flag.String("git-commit", "", "git commit recorded in the manifest")
		captureWait = flag.Duration("capture-wait", 30*time.Second, "how long to wait for a captured insight to become readable")
	)
	flag.Parse()
	if err := run(*url, *credential, *fixtureURL, *fixtureKey, *model, *out, *gitCommit, *replicates, *identityKey, *captureWait); err != nil {
		fmt.Fprintln(os.Stderr, "pkcorpus:", err)
		os.Exit(1)
	}
}

// run wires the clients and executes the corpus run.
func run(url, credential, fixtureURL, fixtureKey, model, out, gitCommit string, replicates, identityKeys int, captureWait time.Duration) error {
	if out == "" {
		return errors.New("-out is required")
	}
	tgt := target.Target{BaseURL: url, Credential: credential}
	runner, err := claudecli.New(claudecli.Options{Model: model})
	if err != nil {
		return err
	}
	ctx := context.Background()
	log := slog.Default()
	version, err := runner.Version(ctx)
	if err != nil {
		return fmt.Errorf("claude --version: %w", err)
	}
	corpus, runErr := pkcorpus.Run(ctx, pkcorpus.Options{
		Target:        tgt,
		IdentityKeys:  identityKeys,
		Fixture:       fixturectl.New(fixtureURL, fixtureKey, 30*time.Second),
		Insights:      lifecycleapi.New(url, tgt.HTTPClient(30*time.Second)),
		Runner:        runner,
		Replicates:    replicates,
		OutDir:        out,
		GitCommit:     gitCommit,
		ClientVersion: version,
		CaptureWait:   captureWait,
		Log:           log,
	})
	if corpus != nil {
		summarize(corpus, out)
	}
	return runErr
}

// summarize prints the run's shape: how many episodes captured anything is
// the only number that decides whether curation has a corpus to work with.
func summarize(c *pkcorpus.Corpus, out string) {
	withCapture := 0
	failed := 0
	for _, ep := range c.Episodes {
		if len(ep.Captured) > 0 {
			withCapture++
		}
		if ep.Error != "" {
			failed++
		}
	}
	fmt.Printf("episodes: %d (captured: %d, failed: %d), insights: %d\narchive: %s\n",
		len(c.Episodes), withCapture, failed, c.Manifest.CapturedTotal, out)
}
