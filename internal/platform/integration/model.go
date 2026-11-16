package integration

import "time"

// The records an operation is read as.

// Binding is the script chosen for an operation and how its runs stand.
type Binding struct {
	ID              string     `json:"id"`
	Operation       string     `json:"operation"`
	ScriptID        string     `json:"script_id"`
	IntervalSeconds int        `json:"interval_seconds"`
	CurrentRunID    string     `json:"current_run_id,omitempty"`
	RetryRequested  bool       `json:"retry_requested"`
	LastStartedAt   *time.Time `json:"last_started_at,omitempty"`
	LastRunID       string     `json:"last_run_id,omitempty"`
	LastStatus      string     `json:"last_status,omitempty"`
	LastReason      string     `json:"last_reason,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	LastFinishedAt  *time.Time `json:"last_finished_at,omitempty"`
	CreatedBy       string     `json:"created_by"`
	UpdatedBy       string     `json:"updated_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// StoredItem is one item as the script was last handed it, and the status it
// last returned.
type StoredItem struct {
	Key       string         `json:"key"`
	Entry     map[string]any `json:"entry"`
	Status    string         `json:"status"`
	Message   string         `json:"message"`
	Removing  bool           `json:"-"`
	FailedRun string         `json:"failed_run,omitempty"`
	CheckedAt *time.Time     `json:"checked_at,omitempty"`
}

// ItemStatus is one item's status as the surfaces that show the item report
// it: under routing, with no run error, which only an administrator reads.
type ItemStatus struct {
	Status    string     `json:"status"`
	Message   string     `json:"message"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// RunID and RunError are the failing run's, filled for an administrator
	// only, when the script itself failed.
	RunID       string `json:"run_id,omitempty"`
	RunScriptID string `json:"run_script_id,omitempty"`
	RunError    string `json:"run_error,omitempty"`
	// ScriptFailed says the script itself failed, so Retry is offered.
	ScriptFailed bool `json:"script_failed,omitempty"`
}

// Overview is one operation as the settings page shows it.
type Overview struct {
	Spec
	Binding    *Binding       `json:"binding,omitempty"`
	ScriptName string         `json:"script_name,omitempty"`
	Running    int            `json:"running_version,omitempty"`
	ApprovedBy string         `json:"approved_by,omitempty"`
	Counts     map[string]int `json:"counts"`
	Items      []StoredItem   `json:"items"`
	// Total is how many items the operation has now, whether or not a script
	// is chosen.
	Total int `json:"total"`
}

// Candidate is a script the picker offers for an operation: its latest
// version defines the operation's function. Running is the version approved
// for handlers (0 for none) and RunningServes whether that one defines it too.
type Candidate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OwnerEmail    string `json:"owner_email"`
	Version       int    `json:"version"`
	Running       int    `json:"running_version"`
	ApprovedBy    string `json:"approved_by,omitempty"`
	RunningServes bool   `json:"running_serves"`
}
