package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Domain-operation instruments (#1898): the platform's own work that a tool
// call or an admin request sets off -- capturing and applying knowledge,
// memory writes, a search, the call catalog, the config store, managed
// resources and archive extraction, table registration, prompt serves -- had
// no count and no duration, and several had no log either. Exposed names:
//
//   - domain_operations_total{operation, result}     result: ok, error
//   - domain_operation_duration_seconds{operation}
//   - knowledge_changes_total{sink, result}          apply_knowledge outcomes
//     and the insights it reviews
//   - search_results_returned_total                  hits search returned
//   - archive_members_extracted_total, archive_extracted_bytes_total
//   - archive_refusals_total{reason}
//
// Every operation also opens a span of the operation's name under the calling
// tool_call (ChildSpan), so its cost is visible inside the call's trace.
//
// Cardinality: operation is the Op* constants internal/opsobs declares, result a closed set,
// sink the apply_knowledge sinks plus "insight", reason the extraction refusal
// classes internal/unarchive names.
const (
	instDomainOps        = "domain_operations"
	instDomainOpDuration = "domain_operation_duration"
	instKnowledgeChanges = "knowledge_changes"
	instSearchResults    = "search_results_returned"
	instArchiveMembers   = "archive_members_extracted"
	instArchiveBytes     = "archive_extracted"
	instArchiveRefusals  = "archive_refusals"
	attrSink             = "sink"
	opResultOK           = "ok"
	opResultError        = "error"
)

// opInstruments are the domain-operation series.
type opInstruments struct {
	ops              metric.Int64Counter
	duration         metric.Float64Histogram
	knowledgeChanges metric.Int64Counter
	searchResults    metric.Int64Counter
	archiveMembers   metric.Int64Counter
	archiveBytes     metric.Int64Counter
	archiveRefusals  metric.Int64Counter
}

// registerOpInstruments registers the domain-operation series.
func (m *Metrics) registerOpInstruments(meter metric.Meter) error {
	o := &m.domain.ops
	var err error
	counter := func(name, desc string, opts ...metric.Int64CounterOption) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var c metric.Int64Counter
		c, err = meter.Int64Counter(name, append([]metric.Int64CounterOption{metric.WithDescription(desc)}, opts...)...)
		err = wrapReg(name, err)
		return c
	}
	o.ops = counter(instDomainOps,
		"Platform operations a tool call or an admin request set off, labeled by operation (knowledge.apply, memory.capture, search.query, calls.record, configstore.write, resource.extract, table.register, prompt.serve, ...) and result (ok, error).")
	o.knowledgeChanges = counter(instKnowledgeChanges,
		"apply_knowledge outcomes and the insights it reviews, labeled by sink (datahub, knowledge_page, agent_instructions, insight) and result (created, applied, approved, rejected, failed).")
	o.searchResults = counter(instSearchResults, "Hits the search tool returned, summed over calls.")
	o.archiveMembers = counter(instArchiveMembers, "Archive members a managed-resource extraction wrote.")
	o.archiveBytes = counter(instArchiveBytes, "Uncompressed bytes a managed-resource extraction wrote.", metric.WithUnit(unitBytes))
	o.archiveRefusals = counter(instArchiveRefusals,
		"Managed-resource extractions refused, labeled by reason (the extraction limit or archive defect internal/unarchive reports).")
	if err != nil {
		return err
	}
	if o.duration, err = meter.Float64Histogram(instDomainOpDuration,
		metric.WithDescription("Seconds a platform operation took, labeled by operation."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instDomainOpDuration, err)
	}
	return nil
}

// Op is one platform operation in progress: its span and its start. The zero
// value is not used; StartOp returns one.
type Op struct {
	m     *Metrics
	name  string
	span  trace.Span
	start time.Time
}

// StartOp opens operation name: a span under the trace ctx carries (none
// when ctx carries no trace) and the start of its duration. End it with End.
// Nil-safe on m: the span still opens, nothing is counted.
func (m *Metrics) StartOp(ctx context.Context, name string) (context.Context, *Op) {
	ctx, span := ChildSpan(ctx, name, trace.WithAttributes(attribute.String(attrOperation, name)))
	return ctx, &Op{m: m, name: name, span: span, start: time.Now()}
}

// End counts the operation under result (ok or error), records
// its duration and ends its span, recording err on it (redacted).
func (o *Op) End(ctx context.Context, err error) {
	result := opResultOK
	status := StatusOK
	if err != nil {
		result, status = opResultError, StatusUpstreamErr
	}
	o.EndResult(ctx, result, status, err)
}

// EndResult is End with the result and span status chosen by the caller: an
// operation that answered a refusal in-band (a tool error result) counts as an
// error without carrying a Go error.
func (o *Op) EndResult(ctx context.Context, result, statusCategory string, err error) {
	SetSpanStatus(o.span, statusCategory, err)
	o.span.End()
	if o.m == nil {
		return
	}
	op := attribute.String(attrOperation, o.name)
	o.m.domain.ops.ops.Add(ctx, 1, metric.WithAttributes(op, attribute.String(attrResult, result)))
	o.m.domain.ops.duration.Record(ctx, time.Since(o.start).Seconds(), metric.WithAttributes(op))
}

// RecordKnowledgeChange records one apply_knowledge outcome. Nil-safe.
func (m *Metrics) RecordKnowledgeChange(ctx context.Context, sink, result string) {
	if m == nil {
		return
	}
	m.domain.ops.knowledgeChanges.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrSink, sink), attribute.String(attrResult, result)))
}

// RecordSearchResults records the hits one search returned. Nil-safe.
func (m *Metrics) RecordSearchResults(ctx context.Context, n int) {
	if m == nil || n <= 0 {
		return
	}
	m.domain.ops.searchResults.Add(ctx, int64(n))
}

// RecordArchiveExtraction records one extraction's members and bytes. Nil-safe.
func (m *Metrics) RecordArchiveExtraction(ctx context.Context, members int, bytes int64) {
	if m == nil {
		return
	}
	m.domain.ops.archiveMembers.Add(ctx, int64(members))
	m.domain.ops.archiveBytes.Add(ctx, bytes)
}

// RecordArchiveRefusal records one refused extraction. Nil-safe.
func (m *Metrics) RecordArchiveRefusal(ctx context.Context, reason string) {
	if m == nil {
		return
	}
	m.domain.ops.archiveRefusals.Add(ctx, 1, metric.WithAttributes(attribute.String(attrReason, reason)))
}
