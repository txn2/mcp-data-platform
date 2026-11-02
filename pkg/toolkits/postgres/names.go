package postgres

import "fmt"

// trinoKind is the Trino connection kind, the one an application database
// shares its name space with.
const trinoKind = "trino"

// NameClash refuses a name an application database and a Trino connection
// would share, and returns "" for any other save. A script's platform.query
// names its connection alone and goes to the application database of that
// name, so a Trino connection sharing it would be out of every script's reach
// (#2428). held reports whether a connection of the given kind holds the name.
func NameClash(kind, name string, held func(kind string) bool) string {
	other := map[string]string{Kind: trinoKind, trinoKind: Kind}[kind]
	if other == "" || !held(other) {
		return ""
	}
	what := map[string]string{Kind: "an application database", trinoKind: "a Trino connection"}[other]
	return fmt.Sprintf("%q is already %s: an application database and a Trino connection cannot share a name, "+
		"because a script's platform.query names its connection alone", name, what)
}
