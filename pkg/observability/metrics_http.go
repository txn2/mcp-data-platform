package observability

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Inbound request instruments (#1889). Exposed names:
//
//   - http_server_request_duration_seconds{route, method, status_class}
//     every request the platform's listeners answer, by the route TEMPLATE
//     it matched ("GET /api/v1/resources", never the path), the HTTP method
//     and the status class; the MCP transport at "/" is split by method so
//     a long-lived GET stream is its own series beside the POST messages
//   - http_rate_limited_total{limiter}  every 429 an HTTP limiter answered,
//     by the limiter's name (internal/httpobs names the set)
//   - mcp_requests_total{method, status} and mcp_request_duration_seconds{method, status}
//     every MCP method other than tools/call (initialize, tools/list,
//     resources/read, ...), which has mcp_tool_calls_total
//
// Cardinality: route is the set of patterns registered on the mux, a closed
// set in the code, plus "unmatched" for a request no pattern answers; a
// caller cannot mint a series by inventing a path. method is the supported
// HTTP methods or the MCP methods the SDK dispatches; both are clamped.
const (
	instHTTPServerDuration = "http_server_request_duration"
	instHTTPRateLimited    = "http_rate_limited"
	instMCPRequests        = "mcp_requests"
	instMCPRequestDuration = "mcp_request_duration"

	attrRoute   = "route"
	attrLimiter = "limiter"
)

// httpInstruments are the inbound request series.
type httpInstruments struct {
	serverDuration     metric.Float64Histogram
	rateLimited        metric.Int64Counter
	mcpRequests        metric.Int64Counter
	mcpRequestDuration metric.Float64Histogram
}

// registerHTTPInstruments registers the inbound request series.
func (m *Metrics) registerHTTPInstruments(meter metric.Meter) error {
	h := &m.http
	var err error
	if h.serverDuration, err = meter.Float64Histogram(instHTTPServerDuration,
		metric.WithDescription("Seconds an inbound HTTP request took to answer, labeled by route template, method and status_class. Measured outside every handler, so it covers authentication and the route's own work."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instHTTPServerDuration, err)
	}
	if h.rateLimited, err = meter.Int64Counter(instHTTPRateLimited,
		metric.WithDescription("Total HTTP requests answered 429 by a rate limiter, labeled by limiter (oauth_token, oauth_register, portal_viewer, portal_content, portal_refs, observability_proxy, pdf_export, webhook, unknown).")); err != nil {
		return wrapReg(instHTTPRateLimited, err)
	}
	if h.mcpRequests, err = meter.Int64Counter(instMCPRequests,
		metric.WithDescription("Total MCP requests other than tools/call the platform answered, labeled by method and status. A tools/call is counted by mcp_tool_calls_total.")); err != nil {
		return wrapReg(instMCPRequests, err)
	}
	if h.mcpRequestDuration, err = meter.Float64Histogram(instMCPRequestDuration,
		metric.WithDescription("Seconds an MCP request other than tools/call took to answer, labeled by method and status."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instMCPRequestDuration, err)
	}
	return nil
}

// RecordHTTPServerRequest records one answered inbound request under its route
// template. Nil-safe. The route is the pattern the mux matched, resolved by
// the caller (internal/httpobs), and the method is clamped by HTTPMethodLabel.
func (m *Metrics) RecordHTTPServerRequest(ctx context.Context, route, method, statusClass string, d time.Duration) {
	if m == nil || m.http.serverDuration == nil {
		return
	}
	m.http.serverDuration.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String(attrRoute, route),
		attribute.String(attrMethod, HTTPMethodLabel(method)),
		attribute.String(attrStatusClass, statusClass)))
}

// RecordHTTPRateLimited counts one 429 under the limiter that answered it. Nil-safe.
func (m *Metrics) RecordHTTPRateLimited(ctx context.Context, limiter string) {
	if m == nil || m.http.rateLimited == nil {
		return
	}
	m.http.rateLimited.Add(ctx, 1, metric.WithAttributes(attribute.String(attrLimiter, limiter)))
}

// RecordMCPRequest records one MCP request other than tools/call. Nil-safe.
// method is the MCP method name the SDK dispatched, which is a closed set
// (shared.go's method table); status is StatusOK or an error status.
func (m *Metrics) RecordMCPRequest(ctx context.Context, method, status string, d time.Duration) {
	if m == nil || m.http.mcpRequests == nil {
		return
	}
	set := metric.WithAttributes(attribute.String(attrMethod, method), attribute.String(attrStatus, status))
	m.http.mcpRequests.Add(ctx, 1, set)
	m.http.mcpRequestDuration.Record(ctx, d.Seconds(), set)
}

// HTTPMethodLabel clamps an HTTP method to the standard set, so a request
// with an invented method cannot mint a series; anything else is unknown.
func HTTPMethodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	}
	return MetricLabelUnknown
}
