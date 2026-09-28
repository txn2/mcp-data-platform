// Package libraryuse is who loads a managed-script library (#1941): one
// script whose current source loads it, and the refusal a delete of a library
// still in use answers with. It is its own package so the script contract in
// pkg/script can carry it without growing that package's public surface, as
// internal/runstate is for the run record.
package libraryuse

import "strings"

// Use is one script whose current source loads a library, and the version of
// the library it loads.
type Use struct {
	ScriptID    string `json:"script_id" example:"3f0c9b1e-8a41-4c55-9d0e-2b7a6c1d4e5f"`
	Name        string `json:"name" example:"weekly-sales"`
	DisplayName string `json:"display_name" example:"Weekly Sales"`
	OwnerEmail  string `json:"owner_email" example:"jane@example.com"`
	Version     int    `json:"version" example:"2"`
}

// InUseError is a refused delete of a library: Users are the scripts whose
// current source loads it. Its message is what the caller is told.
type InUseError struct {
	Users []string
}

func (e *InUseError) Error() string {
	return "this library is loaded by " + strings.Join(e.Users, ", ") +
		", which would fail at their next run, so it was not deleted; change those scripts to stop loading it first"
}
