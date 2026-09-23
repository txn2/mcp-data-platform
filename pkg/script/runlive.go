package script

import "time"

// RunProgress is a running script's latest platform.progress report (#1847):
// a message, and optionally how far through how much. Done and Total are
// pointers because "3 of an unknown number" and "no count at all" are both
// reports a script makes.
type RunProgress struct {
	Message string    `json:"message"`
	Done    *int64    `json:"done,omitempty"`
	Total   *int64    `json:"total,omitempty"`
	At      time.Time `json:"at"`
}

// RunLive is what a claimed run has reported so far, written to its row while
// it executes so a reader sees more than "running" (#1847). Unchanged says
// nothing is new since the last write: the store leaves the row's report as it
// is and only answers the cancel check, rather than rewriting a log that has
// not moved.
type RunLive struct {
	Progress     *RunProgress
	Log          string
	LogTruncated bool
	Unchanged    bool
}

// OutputIdentityKey is the key that makes one (script, output name) pair one
// portal asset: platform.export, platform.publish_data and an export tool
// called inside a run (#1854) all resolve an output name through it, so each
// writes the next version of the same asset. It names the script by ID, so a
// rename keeps its outputs and a later script with the same name does not
// inherit them.
func OutputIdentityKey(scriptID, name string) string {
	return "script:" + scriptID + ":" + name
}
