package admin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/configenv"
)

// maxPlaceholderReport bounds how many offending keys one refusal lists, so a
// pasted file's worth of placeholders does not answer with a wall of text.
const maxPlaceholderReport = 10

// checkNoPlaceholders refuses a database-managed connection config carrying a
// "${...}" placeholder.
//
// The platform expands ${VAR} in its configuration FILE, before that document
// is parsed. A connection stored through this API is not that document: its
// values are used exactly as they arrive, so a placeholder is stored verbatim
// and the connection is unusable from the moment it is created — the store
// answers 200, the connection lists and reads correctly, and every call against
// it fails several layers away at DSN construction with "invalid userinfo"
// (#1805). Refusing here is the difference between learning that at the PUT and
// learning it from a script that already ran half of a pipeline.
//
// It walks nested maps and slices because a connection config is not flat: an
// api connection's static headers and a toolkit's per-tool settings are objects
// of their own, and a placeholder in one of them is the same defect.
func checkNoPlaceholders(config map[string]any) error {
	offenders := map[string]string{}
	walkConfigStrings(config, "", func(path, value string) {
		if frags := configenv.Unexpanded(value); len(frags) > 0 {
			if _, seen := offenders[path]; !seen {
				offenders[path] = frags[0]
			}
		}
	})
	if len(offenders) == 0 {
		return nil
	}

	keys := make([]string, 0, len(offenders))
	for key := range offenders {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	listed := keys
	suffix := ""
	if len(listed) > maxPlaceholderReport {
		suffix = fmt.Sprintf(" (and %d more)", len(listed)-maxPlaceholderReport)
		listed = listed[:maxPlaceholderReport]
	}
	shown := make([]string, len(listed))
	for i, key := range listed {
		shown[i] = fmt.Sprintf("%s=%q", key, offenders[key])
	}

	return fmt.Errorf(
		"%s%s: a ${...} placeholder is stored literally on a database-managed connection, which the platform "+
			"expands only in its configuration file, so this connection would be created and then fail every call. "+
			"Send the resolved value, or declare this connection in the platform configuration file where ${VAR} "+
			"is expanded",
		strings.Join(shown, ", "), suffix)
}

// walkConfigStrings visits every string in a connection config, reporting the
// dotted path it sits at. A slice element's path carries its index, so the
// refusal names one entry of a list rather than the list.
func walkConfigStrings(value any, path string, visit func(path, value string)) {
	switch v := value.(type) {
	case string:
		visit(path, v)
	case map[string]any:
		for key, nested := range v {
			walkConfigStrings(nested, joinConfigPath(path, key), visit)
		}
	case []any:
		for i, nested := range v {
			walkConfigStrings(nested, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

// joinConfigPath appends a key to a dotted config path.
func joinConfigPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
