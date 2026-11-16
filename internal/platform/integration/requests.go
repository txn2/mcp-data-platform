package integration

// What is asked of the service and the store, and what a run left.

// Actor is who changes an integration.
type Actor struct {
	Email string
	Roles []string
	Admin bool
}

// BindRequest chooses an operation's script.
type BindRequest struct {
	Operation string
	ScriptID  string
	// IntervalSeconds is the timer; 0 keeps the current one or the default.
	IntervalSeconds int
	// Approve approves the script's latest version for handlers as the actor.
	Approve bool
}

// NewRun is one integration run to queue.
type NewRun struct {
	ID        string
	ScriptID  string
	VersionID string
	Version   int
	Approver  string
	Roles     []string
	Call      map[string]any
	Reason    string
	Added     []Item
	Removed   []string
}

// RunOutcome is how an integration's run ended.
type RunOutcome struct {
	Finished bool
	Status   string
	Error    string
	Result   []byte
	Call     map[string]any
}

// Applied is what a finished run left: each item's status, and the items it
// removed.
type Applied struct {
	RunID    string
	Results  map[string]ItemResult
	Failed   []string
	FailWith string
	Removed  []string
	Status   string
	Error    string
}
