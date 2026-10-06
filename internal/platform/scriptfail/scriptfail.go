// Package scriptfail reads a managed script's failed tool call the way the
// tool classified it (#2032): whose problem the failure is, whether the next
// run is expected to pass, the sentence a person reads that in, whether the
// host may issue the call again, and the envelope a script asking for its
// failures back (on_error = "return") is handed. mcp-trino's trino_query and
// trino_execute classify every failure in structuredContent.error; the
// classification is read from that envelope and never from the message text.
// Extracted from internal/platform/scriptrun, which holds the interpreter and
// its bindings, for its size budget.
package scriptfail

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptguard"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
)

const (
	// codeTrinoQueryFailed is the code mcp-trino gives every classified
	// query failure.
	codeTrinoQueryFailed = "trino_query_failed"
	// categoryClientInput is the category of a failure the call itself
	// caused: bad SQL, a missing table, a constraint violation.
	categoryClientInput = "client_input"
)

// The values platform.query, platform.execute and platform.call take for
// on_error.
const (
	// OnErrorRaise ends the run on a failed call: the default.
	OnErrorRaise = "raise"
	// OnErrorReturn hands a failed call back to the script as data.
	OnErrorReturn = "return"
)

// classifiedError is a tool failure the tool classified, carrying the sentence
// that says what the failure means for the script's owner: whose problem it is
// and whether the next run is expected to pass. The run's error, the owner's
// email, the session briefing and the portal all show it.
type classifiedError struct {
	err   error
	class string
}

func (e *classifiedError) Error() string { return e.err.Error() + "\n" + e.class }

func (e *classifiedError) Unwrap() error { return e.err }

// Classify reads a failed call's envelope: a failure its tool classified as
// retryable ends the run as the upstream's (cause upstream, retryable), and
// any classified failure carries the sentence describing it. A failure the
// tool did not classify, and one already attributed, is returned unchanged.
func Classify(tool string, err error) error {
	var (
		refusal  *scriptsession.RefusalError
		upstream *scriptguard.UpstreamError
	)
	if err == nil || errors.As(err, &upstream) || !errors.As(err, &refusal) {
		return err
	}
	if _, classified := refusal.Envelope["retryable"].(bool); !classified {
		return err
	}
	wrapped := &classifiedError{err: err, class: Class(refusal.Envelope)}
	if refusal.Retryable {
		return scriptguard.NewUpstreamError(tool, wrapped)
	}
	return wrapped
}

// Retries reports whether the host may issue a read again after this
// refusal: its tool reported the failure as temporary, and the upstream
// itself said so. A failure to reach the upstream at all (transport) is not
// issued again, because the Trino client has already retried the connection
// for up to its own limit before reporting it (txn2/mcp-trino#106).
func Retries(refusal *scriptsession.RefusalError) bool {
	if refusal == nil || !refusal.Retryable {
		return false
	}
	_, transport := refusal.Envelope["transport"].(map[string]any)
	return !transport
}

// Class is the sentence a classified failure is shown with.
func Class(env map[string]any) string {
	source := "The tool"
	if code, _ := env["code"].(string); code == codeTrinoQueryFailed {
		source = "Trino"
	}
	detail := Detail(env)
	retryable, _ := env["retryable"].(bool)
	category, _ := env["category"].(string)
	_, transport := env["transport"].(map[string]any)
	switch {
	case retryable && transport:
		return fmt.Sprintf("%s could not be reached (%s); expected to pass on its next run.", source, detail)
	case retryable:
		return fmt.Sprintf("%s failed for a reason outside the script (%s); expected to pass on its next run.", source, detail)
	case category == categoryClientInput:
		return fmt.Sprintf("%s refused the statement (%s); the next run fails the same way until the script or the data it reads changes.",
			source, detail)
	default:
		return fmt.Sprintf("%s failed for a reason it does not report as temporary (%s); the next run is not expected to pass on its own.",
			source, detail)
	}
}

// Detail names what the upstream reported: Trino's error type and name and
// the SQLSTATE a connector passed through, or how the connection failed.
func Detail(env map[string]any) string {
	if detail := trinoDetail(env); detail != "" {
		return detail
	}
	if detail := transportDetail(env); detail != "" {
		return detail
	}
	if category, _ := env["category"].(string); category != "" {
		return category
	}
	return "unclassified"
}

// trinoDetail is what Trino reported: its error type and name, and the
// SQLSTATE a connector passed through; "" when it reported nothing.
func trinoDetail(env map[string]any) string {
	t, _ := env["trino"].(map[string]any)
	parts := []string{}
	for _, k := range []string{"error_type", "error_name"} {
		if v, _ := t[k].(string); v != "" {
			parts = append(parts, v)
		}
	}
	detail := strings.Join(parts, " / ")
	if state, _ := t["sql_state"].(string); state != "" {
		detail += ", SQLSTATE " + state
	}
	return strings.TrimPrefix(detail, ", ")
}

// transportDetail is how the connection failed when Trino never answered;
// "" when it did.
func transportDetail(env map[string]any) string {
	t, _ := env["transport"].(map[string]any)
	kind, _ := t["kind"].(string)
	if status, ok := t["http_status"].(float64); ok && status > 0 {
		return fmt.Sprintf("%s, HTTP %d", kind, int(status))
	}
	return kind
}

// OnError reads a binding's on_error= argument: true when a failed call is
// to be returned to the script rather than end the run.
func OnError(binding, value string) (bool, error) {
	switch value {
	case "", OnErrorRaise:
		return false, nil
	case OnErrorReturn:
		return true, nil
	default:
		return false, fmt.Errorf("in %s: on_error is %q or %q, not %q", binding, OnErrorRaise, OnErrorReturn, value)
	}
}

// Envelope is the failure a script that asked for it is handed: the tool's
// error envelope, with code, message and retryable always present; false
// when err is not a failed tool call. A call whose upstream did not answer is
// retryable whatever its envelope says, as the run it would have ended is.
func Envelope(err error) (map[string]any, bool) {
	var refusal *scriptsession.RefusalError
	if !errors.As(err, &refusal) {
		return nil, false
	}
	env := make(map[string]any, len(refusal.Envelope))
	maps.Copy(env, refusal.Envelope)
	if _, ok := env["code"].(string); !ok {
		env["code"] = refusal.Code
	}
	if _, ok := env["message"].(string); !ok {
		env["message"] = refusal.Text
	}
	var upstream *scriptguard.UpstreamError
	env["retryable"] = refusal.Retryable || errors.As(err, &upstream)
	return env, true
}
