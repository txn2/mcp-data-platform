package graphprobe

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
)

// SearchToolFullName is the platform's search tool as the episode client
// namespaces it (mcp__<server>__<tool> with claudecli's default server
// name); the no-search arms pass it to --disallowedTools.
const SearchToolFullName = "mcp__bench__search"

// BuildRunner assembles the claude-cli runner for one run's search
// condition: the no-search arms disallow the platform's search tool at the
// client, which is the whole manipulation (the platform is unchanged, and
// fetch is not behind the search-first gate). The returned version string is
// the client as it will actually run, for the manifest.
func BuildRunner(ctx context.Context, model, extraDisallow string, noSearch bool) (*claudecli.Runner, string, error) {
	extra := extraDisallow
	if noSearch {
		if extra == "" {
			extra = SearchToolFullName
		} else {
			extra += "," + SearchToolFullName
		}
	}
	disallowed, err := claudecli.DisallowTools(extra)
	if err != nil {
		return nil, "", err
	}
	runner, err := claudecli.New(claudecli.Options{Model: model, DisallowedTools: disallowed})
	if err != nil {
		return nil, "", err
	}
	version, err := runner.Version(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("claude --version: %w", err)
	}
	return runner, version, nil
}
