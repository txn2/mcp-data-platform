package tablereconcile

import "github.com/txn2/mcp-data-platform/internal/platform/tableregister"

// What a reconcile reports.

// SourceRef names the stored file a location belongs to.
type SourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Exists is whether the platform still holds the file.
	Exists bool `json:"exists"`
}

// Orphan is a table with no registration.
type Orphan struct {
	Table    string `json:"table"`
	Location string `json:"location"`
	// Source is the stored file the location belongs to, when it is one.
	Source *SourceRef `json:"source,omitempty"`
	// Provable is whether the platform can show the table is its own, which
	// is what a repair requires.
	Provable bool `json:"provable"`
}

// Missing is a registration whose table is gone.
type Missing struct {
	Registration tableregister.Registration `json:"registration"`
	// Provable is false for a webhook source's table, which is removed with
	// the source and never by a repair.
	Provable bool `json:"provable"`
}

// Drifted is a registration whose table reads somewhere else.
type Drifted struct {
	Registration tableregister.Registration `json:"registration"`
	Location     string                     `json:"location"`
}

// Reconciliation is a connection's scratch schema against its registrations.
type Reconciliation struct {
	Connection string    `json:"connection"`
	Catalog    string    `json:"catalog"`
	Schema     string    `json:"schema"`
	Orphans    []Orphan  `json:"orphans"`
	Missing    []Missing `json:"missing"`
	Drifted    []Drifted `json:"drifted"`
	// Truncated is whether the schema held more tables than one listing
	// returns, so the orphans are a partial list and none is reported
	// missing.
	Truncated bool `json:"truncated"`
}
