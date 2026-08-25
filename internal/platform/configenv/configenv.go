// Package configenv expands ${VAR} and ${VAR:-default} placeholders in a
// configuration document before it is parsed.
//
// It is its own package rather than a corner of pkg/platform because the
// substitution rule is a contract two audiences depend on -- every shipped
// config writes against it, and every operator reads a failure through it --
// and because pkg/platform is at its size budget: a package that must not grow
// is not where a self-contained rule belongs.
package configenv

import (
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// envVarPattern matches one ${VAR} or ${VAR:-default}. The class stops at the
// first "}", so the syntax does NOT nest: ${A:-${B:-}} is read as ${A:-${B:-}
// followed by a stray "}", and an unset A yields the literal "${B:-}". See
// warnUnexpanded, which is what tells you that happened.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// Expand expands ${VAR} and ${VAR:-default} patterns in the string.
func Expand(s string) string {
	expanded := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		expr := match[2 : len(match)-1]
		// Support ${VAR:-default} syntax.
		if varName, defaultVal, ok := strings.Cut(expr, ":-"); ok {
			if val := os.Getenv(varName); val != "" {
				return val
			}
			return defaultVal
		}
		return os.Getenv(expr)
	})
	warnUnexpanded(expanded)
	return expanded
}

// unexpandedPattern finds a "${" the expansion above did not consume.
var unexpandedPattern = regexp.MustCompile(`\$\{[^\n"]{0,64}`)

// warnUnexpanded reports a "${" still standing after expansion.
//
// It is a warning and not a refusal because free text in a config may contain
// one legitimately -- agent instructions and prompt bodies are part of this
// file. But it is almost always a substitution that did not happen, and the
// failure it produces is remote from its cause: a nested default left
// "${TRINO_USER:-}" as a Trino connection's literal username, which surfaced
// several layers away as "invalid DSN ... invalid userinfo" during a table
// registration. One line at startup naming the fragment is the difference
// between reading that error and reading this one.
func warnUnexpanded(expanded string) {
	seen := make(map[string]bool)
	for _, frag := range unexpandedPattern.FindAllString(expanded, -1) {
		if seen[frag] {
			continue
		}
		seen[frag] = true
		slog.Warn("config: a ${...} placeholder was not expanded; "+
			"the value will be used literally (note that ${A:-${B}} does not nest)",
			"fragment", logsan.SanitizeForLog(frag))
	}
}
