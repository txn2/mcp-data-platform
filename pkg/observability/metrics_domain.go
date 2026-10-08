package observability

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Authentication, configuration and platform-state instruments (#1898). Most
// real fleet problems are a misconfiguration rather than a crash, and before
// these series a misconfiguration was a log line. Exposed names:
//
//   - auth_attempts_total{method, result, reason}   one per validation of a
//     credential by the platform's authenticator chain, and one per browser
//     sign-in; method: oidc, api_key, oauth, browser, unknown
//   - auth_fail_open_total                          requests the HTTP gate let
//     through because the identity provider could not be reached
//   - oidc_jwks_fetches_total{result}, oidc_jwks_fetch_duration_seconds,
//     oidc_jwks_last_success_timestamp_seconds       the OIDC signing-key fetch
//   - oauth_client_registrations_total{result}     dynamic client registration
//   - mcp_tool_call_denials_total{persona, reason}  tool calls the authorizer
//     refused; reason: tool_denied, connection_denied, no_persona
//   - mcp_platform_config_info{toolkit_kinds, auth_methods, tracing,
//     metrics_exporter, sampler_ratio} 1           what the process runs with
//   - config_validation_warnings_total{code}        every configuration warning
//     the platform logged, at boot and on a persona write
//
// And, read on a scrape from the sampler RegisterPlatformState installs:
//
//   - dependency_up{dependency}                     1 when the dependency
//     answered its last probe; a signal only, /readyz does not read it
//   - mcp_platform_connections{kind, state}         connections by probe state
//   - mcp_platform_personas                         personas registered
//   - search_index_last_indexed_age_seconds{kind}   seconds since a source last
//     finished indexing
//
// Cardinality: method, result, reason, dependency, state and code are closed
// sets in the code; persona and kind are operator-defined; the config_info
// labels are one series per process and never carry an address.
const (
	instAuthAttempts       = "auth_attempts"
	instAuthFailOpen       = "auth_fail_open"
	instJWKSFetches        = "oidc_jwks_fetches"
	instJWKSFetchDuration  = "oidc_jwks_fetch_duration"
	instJWKSLastSuccess    = "oidc_jwks_last_success_timestamp"
	instOAuthRegistrations = "oauth_client_registrations"
	instToolDenials        = "mcp_tool_call_denials"
	instConfigInfo         = "mcp_platform_config_info"
	instConfigWarnings     = "config_validation_warnings"
	instDependencyUp       = "dependency_up"
	instConnections        = "mcp_platform_connections"
	instPersonas           = "mcp_platform_personas"
	instSearchIndexAge     = "search_index_last_indexed_age"

	platformStateScrapeLimit = 10 * time.Second

	attrDependency      = "dependency"
	attrCode            = "code"
	attrToolkitKinds    = "toolkit_kinds"
	attrAuthMethods     = "auth_methods"
	attrTracing         = "tracing"
	attrMetricsExporter = "metrics_exporter"
	attrSamplerRatio    = "sampler_ratio"
)

// The values this file records itself; the vocabulary callers pass is in
// internal/opsobs, which keeps it off this package's exported surface.
const (
	authReasonNone = "none"
	resultSuccess  = "success"
	resultFailure  = "failure"
	float64Bits    = 64
)

// ConfigInfo is what mcp_platform_config_info reports. Every field is a
// bounded, non-secret value; RecordConfigInfo drops any value that reads as an
// address (a scheme separator or an @), so a hostname or a DSN cannot reach a
// label even if a caller passes one.
type ConfigInfo struct {
	ToolkitKinds []string
	AuthMethods  []string
	Tracing      bool
	SamplerRatio float64
}

// domainInstruments are the #1898 authentication, configuration and state
// instruments, and the platform-state sampler.
type domainInstruments struct {
	authAttempts     metric.Int64Counter
	authFailOpen     metric.Int64Counter
	jwksFetches      metric.Int64Counter
	jwksDuration     metric.Float64Histogram
	jwksLastSuccess  metric.Float64Gauge
	oauthRegisters   metric.Int64Counter
	toolDenials      metric.Int64Counter
	configInfo       metric.Int64Gauge
	configWarnings   metric.Int64Counter
	dependencyUp     metric.Int64ObservableGauge
	connections      metric.Int64ObservableGauge
	personas         metric.Int64ObservableGauge
	searchIndexAge   metric.Float64ObservableGauge
	ops              opInstruments
	stateMu          sync.RWMutex
	stateSampler     PlatformStateSampler
	configInfoMu     sync.Mutex
	configInfoLabels attribute.Set
}

// registerDomainInstruments registers the #1898 instruments and the scrape
// callback for the platform-state gauges.
func (m *Metrics) registerDomainInstruments(meter metric.Meter) error {
	d := &m.domain
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
	gauge := func(name, desc string) metric.Int64ObservableGauge {
		if err != nil {
			return nil
		}
		var g metric.Int64ObservableGauge
		g, err = meter.Int64ObservableGauge(name, metric.WithDescription(desc))
		err = wrapReg(name, err)
		return g
	}
	d.authAttempts = counter(instAuthAttempts,
		"Credential validations by the platform's authenticator chain and browser sign-ins, labeled by method (oidc, api_key, oauth, browser, unknown), result (success, failure) and reason (none, expired, revoked, bad_signature, unknown_key, idp_unavailable, invalid_claims, no_persona, malformed). Each request surface validates on its own, so one MCP request is counted at the HTTP gate and again by the tool-call middleware.")
	d.authFailOpen = counter(instAuthFailOpen,
		"Requests the MCP HTTP gate passed to the protocol layer because the identity provider could not be reached to validate their credential. The protocol layer refuses their tool calls until it recovers.")
	d.jwksFetches = counter(instJWKSFetches,
		"OIDC signing-key (JWKS) fetches, at startup and on a cache miss, labeled by result (success, failure).")
	d.oauthRegisters = counter(instOAuthRegistrations,
		"OAuth dynamic client registrations, labeled by result (success, failure). The register endpoint's 429s are http_rate_limited_total{limiter=\"oauth_register\"}.")
	d.toolDenials = counter(instToolDenials,
		"Tool calls the persona authorizer refused, labeled by persona and reason (tool_denied, connection_denied, no_persona). A spike after a persona change is usually the change.")
	d.configWarnings = counter(instConfigWarnings,
		"Configuration warnings the platform logged, labeled by code. Counted at boot and again when a persona written through the admin API raises one.")
	d.dependencyUp = gauge(instDependencyUp,
		"1 when a dependency answered its last probe, 0 when it did not, labeled by dependency (database, semantic, query, object_storage, idp, renderer). Absent for a dependency this deployment does not configure. A signal only: /readyz does not read it.")
	d.connections = gauge(instConnections,
		"Connections by kind and state (configured: the kind has no probe; healthy; unreachable; auth_failed), from a probe of every connection on a slow interval.")
	d.personas = gauge(instPersonas, "Personas registered on this replica, file and database personas together.")
	if err != nil {
		return err
	}
	return m.registerDomainTail(meter)
}

// registerDomainTail registers the instruments whose types the helpers above
// do not cover, the operation instruments, and the scrape callback.
func (m *Metrics) registerDomainTail(meter metric.Meter) error {
	d := &m.domain
	var err error
	if d.jwksDuration, err = meter.Float64Histogram(instJWKSFetchDuration,
		metric.WithDescription("Seconds an OIDC signing-key (JWKS) fetch took, discovery included."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instJWKSFetchDuration, err)
	}
	if d.jwksLastSuccess, err = meter.Float64Gauge(instJWKSLastSuccess,
		metric.WithDescription("Unix time of the last successful OIDC signing-key (JWKS) fetch. A value that stops advancing while tokens keep arriving is an identity provider the platform can no longer reach."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instJWKSLastSuccess, err)
	}
	if d.configInfo, err = meter.Int64Gauge(instConfigInfo,
		metric.WithDescription("The process's configuration as 1 under its toolkit kinds, authentication methods, whether tracing is on, the metrics exporter and the trace sampler ratio. Never an address, a hostname or a secret.")); err != nil {
		return wrapReg(instConfigInfo, err)
	}
	if d.searchIndexAge, err = meter.Float64ObservableGauge(instSearchIndexAge,
		metric.WithDescription("Seconds since a search source last finished an index pass, labeled by kind. Every replica reports the same value: read with max."),
		metric.WithUnit(unitSeconds)); err != nil {
		return wrapReg(instSearchIndexAge, err)
	}
	if err := m.registerOpInstruments(meter); err != nil {
		return err
	}
	if _, err := meter.RegisterCallback(m.observePlatformState,
		d.dependencyUp, d.connections, d.personas, d.searchIndexAge); err != nil {
		return fmt.Errorf(instErrFmt, "platform state callback", err)
	}
	return nil
}

// RegisterPlatformState installs the sampler the platform-state gauges are
// read from. Nil-safe; a later call replaces the earlier sampler.
func (m *Metrics) RegisterPlatformState(s PlatformStateSampler) {
	if m == nil {
		return
	}
	m.domain.stateMu.Lock()
	defer m.domain.stateMu.Unlock()
	m.domain.stateSampler = s
}

// observePlatformState is the scrape callback for the platform-state gauges.
func (m *Metrics) observePlatformState(ctx context.Context, o metric.Observer) error {
	m.domain.stateMu.RLock()
	sampler := m.domain.stateSampler
	m.domain.stateMu.RUnlock()
	if sampler == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, platformStateScrapeLimit)
	defer cancel()
	s := sampler(ctx)
	d := &m.domain
	for _, dep := range s.Dependencies {
		var up int64
		if dep.Up {
			up = 1
		}
		o.ObserveInt64(d.dependencyUp, up, metric.WithAttributes(attribute.String(attrDependency, dep.Dependency)))
	}
	for _, c := range s.Connections {
		o.ObserveInt64(d.connections, c.Count, metric.WithAttributes(
			attribute.String(attrKind, c.Kind), attribute.String(attrState, c.State)))
	}
	if s.PersonasKnown {
		o.ObserveInt64(d.personas, s.Personas)
	}
	for _, a := range s.IndexAges {
		o.ObserveFloat64(d.searchIndexAge, a.Age.Seconds(), metric.WithAttributes(attribute.String(attrKind, a.Kind)))
	}
	return nil
}

// RecordAuthAttempt records one credential validation. An empty method records
// MetricLabelUnknown; an empty reason records none. Nil-safe.
func (m *Metrics) RecordAuthAttempt(ctx context.Context, method, result, reason string) {
	if m == nil {
		return
	}
	if method == "" {
		method = MetricLabelUnknown
	}
	if reason == "" {
		reason = authReasonNone
	}
	m.domain.authAttempts.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrMethod, method),
		attribute.String(attrResult, result),
		attribute.String(attrReason, reason),
	))
}

// RecordAuthFailOpen records one request the HTTP gate passed through while
// its credential could not be validated. Nil-safe.
func (m *Metrics) RecordAuthFailOpen(ctx context.Context) {
	if m == nil {
		return
	}
	m.domain.authFailOpen.Add(ctx, 1)
}

// RecordJWKSFetch records one JWKS fetch and, on success, its time as the last
// success. Nil-safe.
func (m *Metrics) RecordJWKSFetch(ctx context.Context, err error, d time.Duration) {
	if m == nil {
		return
	}
	result := resultSuccess
	if err != nil {
		result = resultFailure
	}
	m.domain.jwksFetches.Add(ctx, 1, metric.WithAttributes(attribute.String(attrResult, result)))
	m.domain.jwksDuration.Record(ctx, d.Seconds())
	if err == nil {
		m.domain.jwksLastSuccess.Record(ctx, float64(time.Now().Unix()))
	}
}

// RecordOAuthRegistration records one dynamic client registration. Nil-safe.
func (m *Metrics) RecordOAuthRegistration(ctx context.Context, err error) {
	if m == nil {
		return
	}
	result := resultSuccess
	if err != nil {
		result = resultFailure
	}
	m.domain.oauthRegisters.Add(ctx, 1, metric.WithAttributes(attribute.String(attrResult, result)))
}

// RecordToolDenial records one tool call the authorizer refused. Nil-safe.
func (m *Metrics) RecordToolDenial(ctx context.Context, persona, reason string) {
	if m == nil {
		return
	}
	m.domain.toolDenials.Add(ctx, 1, metric.WithAttributes(
		attribute.String(attrPersona, PersonaLabel(persona)),
		attribute.String(attrReason, reason),
	))
}

// RecordConfigWarning records n configuration warnings under code. Nil-safe.
func (m *Metrics) RecordConfigWarning(ctx context.Context, code string, n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.domain.configWarnings.Add(ctx, n, metric.WithAttributes(attribute.String(attrCode, code)))
}

// RecordConfigInfo sets mcp_platform_config_info to 1 under info's labels. A
// second call replaces the first series' value with 0, so a scrape shows one
// current configuration. Nil-safe.
func (m *Metrics) RecordConfigInfo(ctx context.Context, info ConfigInfo) {
	if m == nil {
		return
	}
	set := attribute.NewSet(
		attribute.String(attrToolkitKinds, joinLabelValues(info.ToolkitKinds)),
		attribute.String(attrAuthMethods, joinLabelValues(info.AuthMethods)),
		attribute.String(attrTracing, strconv.FormatBool(info.Tracing)),
		attribute.String(attrMetricsExporter, exporterLabel(m.cfg.Exporter)),
		attribute.String(attrSamplerRatio, strconv.FormatFloat(info.SamplerRatio, 'g', -1, float64Bits)),
	)
	d := &m.domain
	d.configInfoMu.Lock()
	defer d.configInfoMu.Unlock()
	if d.configInfoLabels.Len() > 0 && !d.configInfoLabels.Equals(&set) {
		d.configInfo.Record(ctx, 0, metric.WithAttributeSet(d.configInfoLabels))
	}
	d.configInfoLabels = set
	d.configInfo.Record(ctx, 1, metric.WithAttributeSet(set))
}

// joinLabelValues renders a set of values as one sorted, deduplicated,
// comma-separated label value, dropping any value labelSafe refuses.
func joinLabelValues(values []string) string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = labelSafe(v); v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ",")
}

// labelSafe returns v, or "" when v reads as an address or a credential: a
// scheme separator, an @, or a character a label value set by code has no use
// for. It is the last guard on mcp_platform_config_info, whose values come
// from configuration.
func labelSafe(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "://") || strings.ContainsAny(v, "@/:?#= ") {
		return ""
	}
	return v
}

// exporterLabel is the metrics exporter as configured, with the unset value
// named as the Prometheus default it means.
func exporterLabel(e MetricsExporter) string {
	if e == "" {
		return string(MetricsExporterPrometheus)
	}
	return labelSafe(string(e))
}
