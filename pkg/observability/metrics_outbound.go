package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Outbound instruments (#1895). Exposed names:
//
//   - http_client_requests_total{kind, connection, status_class} and
//     http_client_request_duration_seconds{kind, connection, status_class}
//     every HTTP request the platform sends, recorded by the one transport
//     chain every first-party client is built from (internal/outbound);
//     kind is what the call is for (api, graphql, mcp, util, oauth, oidc,
//     embedding, notification, spec_fetch, promql, renderer, branding), connection the operator's connection name or empty for a
//     kind that has none, status_class 2xx..5xx or other for a transport
//     failure
//   - egress_blocked_total{reason}  a fetch the egress guard refused, by the
//     class of address it refused (loopback, private, link_local, ...)
//   - upstream_retries_total{kind} and upstream_retries_exhausted_total{kind}
//     a request issued again after a 429 or 503, and a request given up on
//   - embedding_calls_total{model, status}, embedding_call_duration_seconds{model}
//     every embedding call, query-time and indexing alike, and
//     embedding_fallbacks_total{model}, the searches that ranked lexically
//     because the call failed
//   - gateway_upstream_calls_total{connection, outcome} and
//     gateway_upstream_call_duration_seconds{connection}  every tool call the
//     MCP gateway forwarded (ok, tool_error, transport_error, timeout), and
//     gateway_session_redials_total{connection, result}  the re-dials after
//     a dropped upstream session (ok, failed)
//   - upstream_calls_inflight{kind, connection} and
//     upstream_slot_wait_seconds{kind, connection}  the calls this replica
//     holds a slot for on a connection with max_concurrent_calls, and how
//     long each waited for its slot (#2209)
//
// Cardinality: kind and the outcome sets are closed in the code; connection
// and model are the operator's configuration.
const (
	instHTTPClientRequests = "http_client_requests"
	instHTTPClientDuration = "http_client_request_duration"
	instEgressBlocked      = "egress_blocked"
	instUpstreamRetries    = "upstream_retries"
	instUpstreamExhausted  = "upstream_retries_exhausted"
	instEmbeddingCalls     = "embedding_calls"
	instEmbeddingDuration  = "embedding_call_duration"
	instEmbeddingFallbacks = "embedding_fallbacks"
	instGatewayCalls       = "gateway_upstream_calls"
	instGatewayDuration    = "gateway_upstream_call_duration"
	instGatewayRedials     = "gateway_session_redials"
	instUpstreamInflight   = "upstream_calls_inflight"
	instUpstreamSlotWait   = "upstream_slot_wait"

	attrModel = "model"
)

// The outcomes of a forwarded MCP gateway call.
const (
	GatewayOutcomeOK             = "ok"
	GatewayOutcomeToolError      = "tool_error"
	GatewayOutcomeTransportError = "transport_error"
	GatewayOutcomeTimeout        = "timeout"
)

// The results of a re-dial after a dropped upstream session.
const (
	RedialOK     = "ok"
	RedialFailed = "failed"
)

// outboundInstruments are the outbound series.
type outboundInstruments struct {
	requests        metric.Int64Counter
	duration        metric.Float64Histogram
	egressBlocked   metric.Int64Counter
	retries         metric.Int64Counter
	exhausted       metric.Int64Counter
	embedCalls      metric.Int64Counter
	embedDuration   metric.Float64Histogram
	embedFallbacks  metric.Int64Counter
	gatewayCalls    metric.Int64Counter
	gatewayDuration metric.Float64Histogram
	gatewayRedials  metric.Int64Counter
	inflight        metric.Int64UpDownCounter
	slotWait        metric.Float64Histogram
}

// registerOutboundInstruments registers the outbound series.
func (m *Metrics) registerOutboundInstruments(meter metric.Meter) error {
	o := &m.outbound
	var err error
	counter := func(name, desc string) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var c metric.Int64Counter
		c, err = meter.Int64Counter(name, metric.WithDescription(desc))
		err = wrapReg(name, err)
		return c
	}
	histogram := func(name, desc string) metric.Float64Histogram {
		if err != nil {
			return nil
		}
		var h metric.Float64Histogram
		h, err = meter.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unitSeconds))
		err = wrapReg(name, err)
		return h
	}
	o.requests = counter(instHTTPClientRequests,
		"Total HTTP requests the platform sent, labeled by kind, connection and status_class. A transport failure (DNS, dial, TLS, timeout) is status_class other.")
	o.duration = histogram(instHTTPClientDuration,
		"Seconds an outbound HTTP request took, labeled by kind, connection and status_class.")
	o.egressBlocked = counter(instEgressBlocked,
		"Total fetches the egress guard refused, labeled by the class of address refused (loopback, private, link_local, multicast, unspecified, cgnat, embedded_ipv4, internal_hostname).")
	o.retries = counter(instUpstreamRetries,
		"Total requests issued again after an upstream answered 429 or 503, labeled by kind (api for a page walk, script for a managed script's host).")
	o.exhausted = counter(instUpstreamExhausted,
		"Total requests given up on after their retries, labeled by kind.")
	o.embedCalls = counter(instEmbeddingCalls,
		"Total embedding calls, a search's query and an index job's batch alike, labeled by model and status (ok, upstream_err).")
	o.embedDuration = histogram(instEmbeddingDuration,
		"Seconds an embedding call took, labeled by model.")
	o.embedFallbacks = counter(instEmbeddingFallbacks,
		"Total searches ranked lexically because the embedding call failed, labeled by model.")
	o.gatewayCalls = counter(instGatewayCalls,
		"Total tool calls the MCP gateway forwarded to an upstream, labeled by connection and outcome (ok, tool_error, transport_error, timeout).")
	o.gatewayDuration = histogram(instGatewayDuration,
		"Seconds a forwarded MCP gateway tool call took, labeled by connection.")
	o.gatewayRedials = counter(instGatewayRedials,
		"Total re-dials of an upstream MCP session after the upstream dropped it, labeled by connection and result (ok, failed).")
	o.slotWait = histogram(instUpstreamSlotWait,
		"Seconds a call to a connection with max_concurrent_calls waited for a slot before it was sent, labeled by kind and connection (#2209).")
	if err == nil {
		o.inflight, err = meter.Int64UpDownCounter(instUpstreamInflight, metric.WithDescription(
			"Calls this replica holds a slot for on a connection with max_concurrent_calls, labeled by kind and connection (#2209)."))
		err = wrapReg(instUpstreamInflight, err)
	}
	return err
}

// AddUpstreamInflight moves the count of calls this replica holds a slot for
// on a limited connection (#2209). Nil-safe.
func (m *Metrics) AddUpstreamInflight(ctx context.Context, kind, connection string, delta int64) {
	if m == nil || m.outbound.inflight == nil {
		return
	}
	m.outbound.inflight.Add(ctx, delta, metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrConnection, connection)))
}

// RecordUpstreamSlotWait records how long a call waited for a slot on a
// limited connection (#2209). Nil-safe.
func (m *Metrics) RecordUpstreamSlotWait(ctx context.Context, kind, connection string, d time.Duration) {
	if m == nil || m.outbound.slotWait == nil {
		return
	}
	m.outbound.slotWait.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrConnection, connection)))
}

// RecordHTTPClientRequest records one outbound HTTP request. Nil-safe.
func (m *Metrics) RecordHTTPClientRequest(ctx context.Context, kind, connection, statusClass string, d time.Duration) {
	if m == nil || m.outbound.requests == nil {
		return
	}
	set := metric.WithAttributes(
		attribute.String(attrKind, kind),
		attribute.String(attrConnection, connection),
		attribute.String(attrStatusClass, statusClass))
	m.outbound.requests.Add(ctx, 1, set)
	m.outbound.duration.Record(ctx, d.Seconds(), set)
}

// RecordEgressBlocked counts one fetch the egress guard refused. Nil-safe.
func (m *Metrics) RecordEgressBlocked(ctx context.Context, reason string) {
	if m == nil || m.outbound.egressBlocked == nil {
		return
	}
	m.outbound.egressBlocked.Add(ctx, 1, metric.WithAttributes(attribute.String(attrReason, reason)))
}

// RecordUpstreamRetry counts one request issued again, or given up on when
// exhausted is set. Nil-safe.
func (m *Metrics) RecordUpstreamRetry(ctx context.Context, kind string, exhausted bool) {
	if m == nil || m.outbound.retries == nil {
		return
	}
	set := metric.WithAttributes(attribute.String(attrKind, kind))
	if exhausted {
		m.outbound.exhausted.Add(ctx, 1, set)
		return
	}
	m.outbound.retries.Add(ctx, 1, set)
}

// RecordEmbeddingCall records one embedding call under its model. Nil-safe.
func (m *Metrics) RecordEmbeddingCall(ctx context.Context, model, status string, d time.Duration) {
	if m == nil || m.outbound.embedCalls == nil {
		return
	}
	m.outbound.embedCalls.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrModel, model), attribute.String(attrStatus, status)))
	m.outbound.embedDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(attrModel, model)))
}

// RecordEmbeddingFallback counts one search ranked lexically. Nil-safe.
func (m *Metrics) RecordEmbeddingFallback(ctx context.Context, model string) {
	if m == nil || m.outbound.embedFallbacks == nil {
		return
	}
	m.outbound.embedFallbacks.Add(ctx, 1, metric.WithAttributes(attribute.String(attrModel, model)))
}

// RecordGatewayUpstreamCall records one forwarded MCP gateway call. Nil-safe.
func (m *Metrics) RecordGatewayUpstreamCall(ctx context.Context, connection, outcome string, d time.Duration) {
	if m == nil || m.outbound.gatewayCalls == nil {
		return
	}
	m.outbound.gatewayCalls.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrConnection, connection), attribute.String(attrOutcome, outcome)))
	m.outbound.gatewayDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(attrConnection, connection)))
}

// RecordGatewaySessionRedial counts one re-dial after a dropped session. Nil-safe.
func (m *Metrics) RecordGatewaySessionRedial(ctx context.Context, connection, result string) {
	if m == nil || m.outbound.gatewayRedials == nil {
		return
	}
	m.outbound.gatewayRedials.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrConnection, connection), attribute.String(attrResult, result)))
}
