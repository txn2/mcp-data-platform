package graphql

import (
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// maxAuditMessageChars bounds the upstream message an audit row carries.
// The first error's text is the diagnosis an operator greps for; a
// server that puts a stack trace in it does not get the whole trace
// stored on every row.
const maxAuditMessageChars = 1024

// auditVerdict is what the audit middleware is told about a call's
// upstream outcome: the bounded category it classifies the call on, and
// the message it records as the row's error_message when the category
// is not ok.
type auditVerdict struct {
	outcome string
	message string
}

// classifyUpstream derives the audit verdict of one call from what the
// endpoint answered. It is the graphql kind's counterpart of the api
// gateway's ClassifyInvokeOutcome, with one category the gateway has no
// use for: a GraphQL endpoint reports almost every failure as an HTTP
// 200 carrying an errors array, and upstreamFailed already treats that
// as the failure it is. The audit row, the call record and the outbound
// metric classify on the same verdict, which is what #1678 asked for:
// a call whose result says upstream_error is not a success anywhere
// the platform records it.
//
// The message names the first upstream error when there is one, since
// that is the only diagnosis the caller got; a non-2xx with no GraphQL
// body is named by its status text, as the gateway names one.
func classifyUpstream(status int, errs []Error, failed bool) auditVerdict {
	if !failed {
		return auditVerdict{outcome: observability.OutcomeOK}
	}
	v := auditVerdict{outcome: observability.OutcomeUpstreamError, message: firstErrorMessage(errs)}
	switch {
	case status >= http.StatusInternalServerError:
		v.outcome = observability.OutcomeUpstream5xx
	case status >= http.StatusBadRequest:
		v.outcome = observability.OutcomeUpstream4xx
	}
	if v.message == "" {
		v.message = http.StatusText(status)
	}
	if v.message == "" {
		v.message = fmt.Sprintf("HTTP %d", status)
	}
	return v
}

// firstErrorMessage renders the errors array as one audit message: the
// first error's own text, bounded, and how many more followed it.
func firstErrorMessage(errs []Error) string {
	if len(errs) == 0 {
		return ""
	}
	msg := errs[0].Message
	if utf8.RuneCountInString(msg) > maxAuditMessageChars {
		runes := []rune(msg)
		msg = string(runes[:maxAuditMessageChars]) + "..."
	}
	if msg == "" {
		msg = "the endpoint reported an error without a message"
	}
	if rest := len(errs) - 1; rest > 0 {
		msg = fmt.Sprintf("%s (and %d more)", msg, rest)
	}
	return msg
}

// stampAuditOutcome puts the verdict on the result's _meta, where the
// audit middleware reads it. It is set on every call, success included,
// as the api gateway's is; the middleware treats ok as no override.
func stampAuditOutcome(result *mcp.CallToolResult, v auditVerdict) {
	if result.Meta == nil {
		result.Meta = mcp.Meta{}
	}
	result.Meta[observability.MetaAuditOutcome] = v.outcome
	if v.message != "" {
		result.Meta[observability.MetaAuditOutcomeMessage] = v.message
	}
}
