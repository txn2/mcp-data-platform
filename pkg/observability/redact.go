package observability

import (
	"errors"
	"regexp"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// maxRedactedBytes bounds the error text a span event carries. An upstream
// error can quote a whole SQL statement or response body; a trace backend
// stores every byte of it per span.
const maxRedactedBytes = 256

// redactedLiteral stands in for a quoted literal an error message carried.
const redactedLiteral = "'?'"

// redactedEmail stands in for an email address an error message carried.
const redactedEmail = "[email]"

// quotedLiteral matches a single-, double- or backtick-quoted literal. A
// quoted value in an error is the caller's or the data's: the column a query
// could not resolve, the row value a cast refused, the key a lookup missed.
//
//nolint:gochecknoglobals // a compiled pattern is immutable configuration.
var quotedLiteral = regexp.MustCompile("'[^']*'|\"[^\"]*\"|`[^`]*`")

// emailAddress matches an email address in free text so a person's address
// never leaves the platform on a span, whatever layer put it in the message.
//
//nolint:gochecknoglobals // a compiled pattern is immutable configuration.
var emailAddress = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// RedactMessage returns the form of an error message that may leave the
// platform on telemetry (#1892): quoted literals and email addresses are
// replaced, control characters are stripped, and the result is cut at
// maxRedactedBytes. The error's shape survives (which operation failed and
// why); the values it carried do not.
func RedactMessage(msg string) string {
	msg = quotedLiteral.ReplaceAllString(msg, redactedLiteral)
	msg = emailAddress.ReplaceAllString(msg, redactedEmail)
	msg = strings.Join(strings.Fields(msg), " ")
	return logsan.Excerpt(msg, maxRedactedBytes)
}

// RedactError returns an error whose text is RedactMessage of err's. It is
// what SetSpanStatus records on the span; the original error is for the
// caller, the audit row and the log line, which stay inside the platform.
// A nil err yields nil.
func RedactError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(RedactMessage(err.Error()))
}
