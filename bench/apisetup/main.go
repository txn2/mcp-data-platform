// Command apisetup registers the API-connection study's fixtures with a
// running platform (issue #1027). Modes: b0 registers the per-endpoint
// MCP connection; b1 registers the catalog, the tier spec, and the api
// connection (with -wait-embed for the b1-hyb readiness gate).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisetup"
)

func main() {
	mode := flag.String("mode", "", "b0 (per-endpoint mcp connection) or b1 (catalog + spec + api connection), required")
	platformURL := flag.String("url", "http://localhost:8098", "platform base URL")
	adminKey := flag.String("credential", os.Getenv("API_KEY_ADMIN"), "admin API key (Bearer)")
	specPath := flag.String("spec", "specs/t0.json", "tier spec fixture (b1)")
	fixtureURL := flag.String("fixture", "http://127.0.0.1:8110", "fixture service base URL (b1)")
	fixtureKey := flag.String("fixture-key", os.Getenv("APISVC_KEY"), "fixture service X-API-Key (b1)")
	epmcpURL := flag.String("epmcp", "http://127.0.0.1:8111", "per-endpoint MCP server URL (b0)")
	waitEmbed := flag.Duration("wait-embed", 0, "b1: wait up to this long for the embedding index to cover every operation (0 = no wait; use for b1-lex)")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout")
	flag.Parse()
	if err := run(*mode, *platformURL, *adminKey, *specPath, *fixtureURL, *fixtureKey, *epmcpURL, *waitEmbed, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "apisetup:", err)
		os.Exit(1)
	}
}

// run executes the selected registration mode.
func run(mode, platformURL, adminKey, specPath, fixtureURL, fixtureKey, epmcpURL string, waitEmbed, timeout time.Duration) error {
	if adminKey == "" {
		return errors.New("admin credential required (-credential or API_KEY_ADMIN)")
	}
	client := apisetup.New(platformURL, adminKey, timeout)
	ctx := context.Background()
	switch mode {
	case "b0":
		if err := client.RegisterB0(ctx, epmcpURL); err != nil {
			return err
		}
		log.Printf("apisetup: mcp connection %q -> %s registered", apisetup.ConnectionName, epmcpURL)
		return nil
	case "b1":
		return runB1(ctx, client, specPath, fixtureURL, fixtureKey, waitEmbed)
	default:
		return fmt.Errorf("unknown -mode %q (want b0 or b1)", mode)
	}
}

// runB1 registers the search-then-invoke arm and optionally waits for
// the embedding index to drain.
func runB1(ctx context.Context, client *apisetup.Client, specPath, fixtureURL, fixtureKey string, waitEmbed time.Duration) error {
	spec, err := os.ReadFile(specPath) // #nosec G304 -- operator-supplied spec fixture path
	if err != nil {
		return err
	}
	if err := client.RegisterB1(ctx, string(spec), fixtureURL, fixtureKey); err != nil {
		return err
	}
	log.Printf("apisetup: catalog %q + api connection %q -> %s registered (%s)",
		apisetup.CatalogID, apisetup.ConnectionName, fixtureURL, specPath)
	if waitEmbed <= 0 {
		return nil
	}
	wantOps := tierOpsForSpec(specPath)
	waitCtx, cancel := context.WithTimeout(ctx, waitEmbed)
	defer cancel()
	if err := client.WaitEmbedDrain(waitCtx, wantOps, 5*time.Second); err != nil {
		return err
	}
	log.Printf("apisetup: embedding index covers all %d operations", wantOps)
	return nil
}

// tierOpsForSpec resolves the expected operation count from the spec
// filename's tier, so the embed-drain gate knows what "complete" means.
func tierOpsForSpec(specPath string) int {
	c := apigen.BuildCatalog()
	for tier, name := range apigen.TierNames() {
		if strings.Contains(specPath, name+".json") {
			return len(c.TierOperations(tier))
		}
	}
	// Unknown filename: fall back to the largest tier (waits for the
	// full catalog).
	return len(c.TierOperations(apigen.Tier2))
}
