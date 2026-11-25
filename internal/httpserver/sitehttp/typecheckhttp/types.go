package typecheckhttp

import "github.com/txn2/mcp-data-platform/internal/site/sitetypecheck"

// TypecheckView is a release's newest type check (#2793); Check is null when
// none has run.
type TypecheckView struct {
	Release int                  `json:"release"`
	Check   *sitetypecheck.Check `json:"check"`
}

// TypecheckSummaries are the newest check of each release, by number, for
// the Releases tab's column.
type TypecheckSummaries struct {
	Releases map[string]sitetypecheck.Summary `json:"releases"`
}

// TypecheckBody names the environment whose incremental state a re-run uses.
type TypecheckBody struct {
	Environment string `json:"environment,omitempty" example:"dev"`
}
