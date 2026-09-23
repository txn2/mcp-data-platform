// Package scriptlex holds the lexical checks a managed script's source is put
// through before it is parsed: the Python instincts an agent brings to a
// Starlark file, answered with what to write instead, and the credential-shaped
// strings that must never be stored in one.
//
// It reads text and nothing else. The checks that need the parse tree -- what
// a script reaches, the reserved words, the destinations -- stay with the
// validator in internal/platform/scriptrun, which folds these findings into
// its report.
package scriptlex

import (
	"regexp"
	"strings"
)

// Finding severities. An error means the script cannot run as written; a
// warning means it will run but somebody should look.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Finding is one thing a lexical check noticed, with the corrective action.
type Finding struct {
	Severity string `json:"severity"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// sourcePattern is a lexical check run over the raw source, before parsing.
// Some of these parse cleanly and are still wrong (an f-string is a name
// followed by a string; datetime is merely an undefined name), and some make
// the parser produce a message that names a token rather than the mistake — so
// catching them on the text gives the author a specific answer either way.
type sourcePattern struct {
	re       *regexp.Regexp
	severity string
	message  string
	hint     string
}

// sourcePatterns are the author-facing lexical checks: the Python instincts an
// agent brings to a Starlark file, and the secret-shaped strings that must
// never be pasted into one.
var sourcePatterns = []sourcePattern{
	{
		re:       regexp.MustCompile(`(?m)^\s*(?:import\s+\w|from\s+\w+\s+import\b)`),
		severity: SeverityError,
		message:  "`import` is not available",
		hint:     "There is no module system. Data comes from `platform.query`; `json`, `xml` and `date` are already predeclared.",
	},
	{
		re:       regexp.MustCompile(`(?m)^\s*(?:try|except|finally)\s*:`),
		severity: SeverityError,
		message:  "`try`/`except` does not exist",
		hint:     "Errors fail the run by design, so a failure is recorded rather than hidden. Check a value before using it, or stop deliberately with `fail(\"message\")`.",
	},
	{
		re:       regexp.MustCompile(`\bf"|\bf'`),
		severity: SeverityWarning,
		message:  "f-strings are not supported",
		hint:     "Use `\"total: {}\".format(n)` or `\"total: %d\" % n`.",
	},
	{
		re:       regexp.MustCompile(`\b(?:datetime|time\.time|date\.today|now\(\))`),
		severity: SeverityWarning,
		message:  "there is no clock in a script",
		hint:     "Reading a clock would make the run unreproducible. The fire time is pinned on `run.fire_time`; derive dates from it with `date.of(run.fire_time)` and the `date` helpers.",
	},
	{
		re:       regexp.MustCompile(`\brandom\.\w`),
		severity: SeverityWarning,
		message:  "there is no randomness in a script",
		hint:     "A random value would make the run unreproducible. Derive what you need from `run.run_id` or from the data itself.",
	},
	{
		re:       regexp.MustCompile(`\b(?:open|__import__)\s*\(|\bos\.\w|\brequests\.\w`),
		severity: SeverityWarning,
		message:  "there is no filesystem or network access in a script",
		hint:     "The platform is the only outside world a script has: read with `platform.query`, write with `platform.export`, and reach anything else — an external API, an object store, another tool — with `platform.call(tool, args)` through a configured connection.",
	},
}

// secretPattern is a credential-shaped string that must never appear in script
// source. Connections are named and their credentials stay in platform
// connection config, so a literal here is either a mistake or an attempt to
// carry authority the script was never given; either way it must be surfaced
// before the source is stored.
//
// Severity follows confidence. A pattern that matches a specific credential
// FORMAT (a private-key header, an AWS key id, a provider token) is an error
// and blocks the save, because nothing else looks like that. A pattern that
// matches a NAMING convention is a warning: `password = 'hunter22'` is a
// credential in Go source and an ordinary predicate inside a SQL string, and
// this scanner cannot tell those apart, so blocking would refuse to store a
// legitimate query with no way around it.
type secretPattern struct {
	re       *regexp.Regexp
	message  string
	severity string
}

// secretPatterns are matched against the raw source. They are shaped to catch
// the credential forms that actually get pasted, and each match is reported as
// an ERROR: a script carrying an inline credential does not get to run while
// someone decides whether it was serious.
var secretPatterns = []secretPattern{
	{regexp.MustCompile(`-{5}BEGIN [A-Z ]*PRIVATE KEY-{5}`), "a private key is embedded in the source", SeverityError},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "an AWS access key id is embedded in the source", SeverityError},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), "a GitHub token is embedded in the source", SeverityError},
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), "a Slack token is embedded in the source", SeverityError},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "a JSON Web Token is embedded in the source", SeverityError},
	{regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s:/@"']+:[^\s:/@"']+@`), "a URL with embedded credentials is in the source", SeverityError},
	{regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|access[_-]?token)\s*=\s*["'][^"']{6,}["']`), "a credential-shaped assignment is in the source; if this is a SQL predicate rather than a credential, it is fine", SeverityWarning},
}

// secretHint is the one corrective action for every secret finding.
const secretHint = "Credentials never belong in a script. Name a connection instead; the platform holds its credentials and authorizes the call."

// Scan runs the lexical checks. The author-facing ones read the code alone
// (CodeOnly); the secret ones read the raw source, because a pasted credential
// is inside a string literal.
func Scan(source string) []Finding {
	var findings []Finding
	code := CodeOnly(source)
	for _, p := range sourcePatterns {
		if loc := p.re.FindStringIndex(code); loc != nil {
			findings = append(findings, Finding{
				Severity: p.severity, Line: lineOf(code, loc[0]),
				Message: p.message, Hint: p.hint,
			})
		}
	}
	for _, p := range secretPatterns {
		if loc := p.re.FindStringIndex(source); loc != nil {
			findings = append(findings, Finding{
				Severity: p.severity, Line: lineOf(source, loc[0]),
				Message: p.message, Hint: secretHint,
			})
		}
	}
	return findings
}

// lineOf reports the 1-based line containing byte offset off.
func lineOf(source string, off int) int {
	if off > len(source) {
		off = len(source)
	}
	return 1 + strings.Count(source[:off], "\n")
}
