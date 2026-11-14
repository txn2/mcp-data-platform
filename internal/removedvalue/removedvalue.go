// Package removedvalue keeps, for each tool, the argument values it no longer
// takes and what replaces each (#2441): manage_script's endpoint_set and
// schedule_set, removed with no alias. A tool's input schema no longer lists a
// removed value, so the SDK refuses a call naming one before the tool's handler
// runs; the error contract (pkg/middleware) reads this registry to answer that
// refusal with the replacement instead of the schema's list of values.
package removedvalue

import "sync"

var values sync.Map

func key(tool, arg, value string) string { return tool + "\x00" + arg + "\x00" + value }

// Register records that tool's arg no longer takes value, and what a caller
// uses instead.
func Register(tool, arg, value, replacement string) {
	values.Store(key(tool, arg, value), replacement)
}

// Lookup is what replaces value of tool's arg, and whether it was removed.
func Lookup(tool, arg, value string) (string, bool) {
	replacement, ok := values.Load(key(tool, arg, value))
	if !ok {
		return "", false
	}
	s, _ := replacement.(string)
	return s, true
}
