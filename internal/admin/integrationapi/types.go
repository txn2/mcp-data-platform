package integrationapi

import "github.com/txn2/mcp-data-platform/internal/platform/integration"

// The bodies the routes read and answer.

// IntegrationList is the list response.
type IntegrationList struct {
	Integrations []integration.Overview `json:"integrations"`
}

// CandidateList is the picker's scripts.
type CandidateList struct {
	Scripts []integration.Candidate `json:"scripts"`
}

// BindInput chooses an operation's script.
type BindInput struct {
	ScriptID string `json:"script_id"`
	// IntervalSeconds is the timer, 60 to 86400; 0 or omitted keeps the
	// current one, 900 for a new binding.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// Approve approves the script's latest version for handlers, as you.
	Approve bool `json:"approve,omitempty"`
}

// Done is the answer to a clear or a retry.
type Done struct {
	Operation string `json:"operation"`
	Done      bool   `json:"done"`
}
