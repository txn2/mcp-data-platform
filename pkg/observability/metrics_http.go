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
//   - endpoint_requests_total{endpoint, status_class} and
//     endpoint_request_duration_seconds{endpoint}  every request a script
//     bound to /endpoints/{name} answered (#2172), by the endpoint's name
//   - site_requests_total{kind, site, environment, status_class} and
//     site_request_duration_seconds{kind, site, environment}  every request
//     the site and page gateway answered for a site or an application's page
//     origin (#2597): kind is site or pages, site the site's or the
//     application's name, environment the site's environment (empty for a
//     page origin), all read from the route the gateway resolved, never from
//     the Host header, so a hostname nobody added mints no series
//   - site_cache_reads_total{outcome}  every file body the gateway read, by
//     where it came from: memory, disk, object_store, or range (a file too
//     large to cache, read by ranged GETs)
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
	instEndpointRequests   = "endpoint_requests"
	instEndpointDuration   = "endpoint_request_duration"
	instSiteRequests       = "site_requests"
	instSiteDuration       = "site_request_duration"
	instSiteCacheReads     = "site_cache_reads"

	attrRoute    = "route"
	attrLimiter  = "limiter"
	attrEndpoint = "endpoint"
	attrSite     = "site"
	attrEnv      = "environment"
)

// httpInstruments are the inbound request series.
type httpInstruments struct {
	serverDuration     metric.Float64Histogram
	rateLimited        metric.Int64Counter
	mcpRequests        metric.Int64Counter
	mcpRequestDuration metric.Float64Histogram
	endpointRequests   metric.Int64Counter
	endpointDuration   metric.Float64Histogram
	siteRequests       metric.Int64Counter
	siteDuration       metric.Float64Histogram
	siteCacheReads     metric.Int64Counter
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
		metric.WithDescription("Total HTTP requests answered 429 by a rate limiter, labeled by limiter (oauth_token, oauth_register, portal_viewer, portal_content, portal_refs, observability_proxy, pdf_export, webhook, endpoint, unknown).")); err != nil {
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
	if h.endpointRequests, err = meter.Int64Counter(instEndpointRequests,
		metric.WithDescription("Total requests a script bound to an endpoint answered, labeled by endpoint (the binding's name) and status_class.")); err != nil {
		return wrapReg(instEndpointRequests, err)
	}
	if h.endpointDuration, err = meter.Float64Histogram(instEndpointDuration,
		metric.WithDescription("Seconds an endpoint took to answer a request, from its arrival to the response, labeled by endpoint."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instEndpointDuration, err)
	}
	return m.registerSiteInstruments(meter)
}

// registerSiteInstruments registers the site and page gateway's series (#2597).
func (m *Metrics) registerSiteInstruments(meter metric.Meter) error {
	h := &m.http
	var err error
	if h.siteRequests, err = meter.Int64Counter(instSiteRequests,
		metric.WithDescription("Total requests the site and page gateway answered for a site or an application's page origin, labeled by kind (site, pages), site, environment and status_class.")); err != nil {
		return wrapReg(instSiteRequests, err)
	}
	if h.siteDuration, err = meter.Float64Histogram(instSiteDuration,
		metric.WithDescription("Seconds the site and page gateway took to answer a request, labeled by kind, site and environment."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instSiteDuration, err)
	}
	if h.siteCacheReads, err = meter.Int64Counter(instSiteCacheReads,
		metric.WithDescription("Total file bodies the site gateway read, labeled by outcome: memory, disk, object_store, or range (a file too large to cache).")); err != nil {
		return wrapReg(instSiteCacheReads, err)
	}
	return nil
}

// RecordSiteRequest records one request the site and page gateway answered.
// Nil-safe. kind, site and environment come from the route the gateway
// resolved (names an administrator gave), never from the request. One
// argument per label, as RecordEndpointRequest: a struct for them would be
// one more identifier on this package's budgeted exported surface.
func (m *Metrics) RecordSiteRequest(ctx context.Context, kind, site, environment, statusClass string, d time.Duration) { //nolint:revive // argument-limit: one argument per label
	if m == nil || m.http.siteRequests == nil {
		return
	}
	m.http.siteRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrSite, site),
		attribute.String(attrEnv, environment), attribute.String(attrStatusClass, statusClass)))
	m.http.siteDuration.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String(attrKind, kind), attribute.String(attrSite, site), attribute.String(attrEnv, environment)))
}

// RecordSiteCacheRead counts one file body the site gateway read, by where it
// came from. Nil-safe. outcome is one of the gateway's constants.
func (m *Metrics) RecordSiteCacheRead(ctx context.Context, outcome string) {
	if m == nil || m.http.siteCacheReads == nil {
		return
	}
	m.http.siteCacheReads.Add(ctx, 1, metric.WithAttributes(attribute.String(attrOutcome, outcome)))
}

// RecordEndpointRequest records one request an endpoint answered. Nil-safe.
// endpoint is a binding's name, which an administrator sets, never a path a
// caller sent.
func (m *Metrics) RecordEndpointRequest(ctx context.Context, endpoint, statusClass string, d time.Duration) {
	if m == nil || m.http.endpointRequests == nil {
		return
	}
	m.http.endpointRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrEndpoint, endpoint), attribute.String(attrStatusClass, statusClass)))
	m.http.endpointDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String(attrEndpoint, endpoint)))
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
// Each case returns the constant rather than the request's own string, so
// the value a label or a log line carries is never the caller's bytes.
func HTTPMethodLabel(method string) string {
	switch method {
	case http.MethodGet:
		return http.MethodGet
	case http.MethodHead:
		return http.MethodHead
	case http.MethodPost:
		return http.MethodPost
	case http.MethodPut:
		return http.MethodPut
	case http.MethodPatch:
		return http.MethodPatch
	case http.MethodDelete:
		return http.MethodDelete
	case http.MethodConnect:
		return http.MethodConnect
	case http.MethodOptions:
		return http.MethodOptions
	case http.MethodTrace:
		return http.MethodTrace
	}
	return MetricLabelUnknown
}
