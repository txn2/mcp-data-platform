package observability

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestDomainInstruments_Exposed records one of every #1898 observation and
// reads the scrape, so the names the documentation carries are the ones
// exported.
func TestDomainInstruments_Exposed(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()

	m.RecordAuthAttempt(ctx, "oidc", "failure", "expired")
	m.RecordAuthAttempt(ctx, "", "failure", "")
	m.RecordAuthFailOpen(ctx)
	m.RecordJWKSFetch(ctx, nil, 20*time.Millisecond)
	m.RecordJWKSFetch(ctx, errors.New("down"), time.Millisecond)
	m.RecordOAuthRegistration(ctx, nil)
	m.RecordOAuthRegistration(ctx, errors.New("bad redirect"))
	m.RecordToolDenial(ctx, "analyst", "tool_denied")
	m.RecordToolDenial(ctx, "", "no_persona")
	m.RecordConfigWarning(ctx, "persona_tool_unregistered", 2)
	m.RecordConfigWarning(ctx, "ignored", 0)
	m.RecordKnowledgeChange(ctx, "datahub", "applied")
	m.RecordSearchResults(ctx, 7)
	m.RecordSearchResults(ctx, 0)
	m.RecordArchiveExtraction(ctx, 3, 4096)
	m.RecordArchiveRefusal(ctx, "max_ratio")
	_, op := m.StartOp(ctx, "search.query")
	op.End(ctx, nil)
	_, op = m.StartOp(ctx, "table.register")
	op.End(ctx, errors.New("refused"))
	_, op = m.StartOp(ctx, "knowledge.apply")
	op.EndResult(ctx, "error", StatusClientErr, nil)

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`auth_attempts_total{method="oidc",reason="expired",result="failure"} 1`,
		`auth_attempts_total{method="unknown",reason="none",result="failure"} 1`,
		`auth_fail_open_total 1`,
		`oidc_jwks_fetches_total{result="success"} 1`,
		`oidc_jwks_fetches_total{result="failure"} 1`,
		`oidc_jwks_fetch_duration_seconds_count 2`,
		`oidc_jwks_last_success_timestamp_seconds `,
		`oauth_client_registrations_total{result="success"} 1`,
		`oauth_client_registrations_total{result="failure"} 1`,
		`mcp_tool_call_denials_total{persona="analyst",reason="tool_denied"} 1`,
		`mcp_tool_call_denials_total{persona="unknown",reason="no_persona"} 1`,
		`config_validation_warnings_total{code="persona_tool_unregistered"} 2`,
		`knowledge_changes_total{result="applied",sink="datahub"} 1`,
		`search_results_returned_total 7`,
		`archive_members_extracted_total 3`,
		`archive_extracted_bytes_total 4096`,
		`archive_refusals_total{reason="max_ratio"} 1`,
		`domain_operations_total{operation="search.query",result="ok"} 1`,
		`domain_operations_total{operation="table.register",result="error"} 1`,
		`domain_operations_total{operation="knowledge.apply",result="error"} 1`,
		`domain_operation_duration_seconds_count{operation="search.query"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	if strings.Contains(body, `code="ignored"`) {
		t.Error("a zero warning count minted a series")
	}
}

// TestRecordConfigInfo_NoAddressInAnyLabel holds the acceptance sentence:
// mcp_platform_config_info carries no label value with "://" or "@", even
// when the caller hands it one.
func TestRecordConfigInfo_NoAddressInAnyLabel(t *testing.T) {
	m := newEnabledMetrics(t)
	m.RecordConfigInfo(context.Background(), ConfigInfo{
		ToolkitKinds: []string{"trino", "s3", "trino", "postgres://user:pw@db:5432/x", "api@host"},
		AuthMethods:  []string{"oidc", "https://idp.example.com/realms/x"},
		Tracing:      true,
		SamplerRatio: 0.25,
	})
	body := scrapeMetrics(t, m.Handler())
	line := seriesLine(t, body, "mcp_platform_config_info{")
	for _, want := range []string{
		`toolkit_kinds="s3,trino"`, `auth_methods="oidc"`, `tracing="true"`,
		`metrics_exporter="prometheus"`, `sampler_ratio="0.25"`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("config info %q missing %s", line, want)
		}
	}
	for _, v := range labelValues(line) {
		if strings.Contains(v, "://") || strings.Contains(v, "@") {
			t.Errorf("label value %q reads as an address", v)
		}
	}
}

// TestRecordConfigInfo_ReplacesSeries shows a second record leaves one current
// configuration at 1 and the earlier one at 0.
func TestRecordConfigInfo_ReplacesSeries(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()
	m.RecordConfigInfo(ctx, ConfigInfo{ToolkitKinds: []string{"trino"}})
	m.RecordConfigInfo(ctx, ConfigInfo{ToolkitKinds: []string{"trino"}})
	m.RecordConfigInfo(ctx, ConfigInfo{})
	body := scrapeMetrics(t, m.Handler())
	if !strings.Contains(body, `toolkit_kinds="trino",tracing="false"} 0`) {
		t.Errorf("the replaced configuration is not 0:\n%s", grepLines(body, "mcp_platform_config_info"))
	}
	if !strings.Contains(body, `toolkit_kinds="none",tracing="false"} 1`) {
		t.Errorf("the current configuration is not 1:\n%s", grepLines(body, "mcp_platform_config_info"))
	}
}

// TestPlatformState_Observed reads every sampled gauge from the sampler.
func TestPlatformState_Observed(t *testing.T) {
	m := newEnabledMetrics(t)
	m.RegisterPlatformState(func(context.Context) PlatformStateSample {
		return PlatformStateSample{
			Dependencies:  []DependencySample{{Dependency: "database", Up: true}, {Dependency: "idp", Up: false}},
			Connections:   []ConnectionStateCount{{Kind: "trino", State: "unreachable", Count: 2}},
			Personas:      3,
			PersonasKnown: true,
			IndexAges:     []IndexAgeSample{{Kind: "tools", Age: 90 * time.Second}},
		}
	})
	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`dependency_up{dependency="database"} 1`,
		`dependency_up{dependency="idp"} 0`,
		`mcp_platform_connections{kind="trino",state="unreachable"} 2`,
		`mcp_platform_personas 3`,
		`search_index_last_indexed_age_seconds{kind="tools"} 90`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

// TestPlatformState_NoSamplerNoSeries shows the gauges are absent until a
// sampler is registered, and that an unknown persona count is not reported.
func TestPlatformState_NoSamplerNoSeries(t *testing.T) {
	m := newEnabledMetrics(t)
	if body := scrapeMetrics(t, m.Handler()); strings.Contains(body, "dependency_up{") {
		t.Error("dependency_up reported with no sampler")
	}
	m.RegisterPlatformState(func(context.Context) PlatformStateSample { return PlatformStateSample{} })
	if body := scrapeMetrics(t, m.Handler()); strings.Contains(body, "mcp_platform_personas ") {
		t.Error("a persona count reported without a registry")
	}
}

func TestDomainInstruments_NilSafe(t *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.RecordAuthAttempt(ctx, "oidc", "success", "none")
	m.RecordAuthFailOpen(ctx)
	m.RecordJWKSFetch(ctx, nil, time.Second)
	m.RecordOAuthRegistration(ctx, nil)
	m.RecordToolDenial(ctx, "p", "tool_denied")
	m.RecordConfigWarning(ctx, "c", 1)
	m.RecordConfigInfo(ctx, ConfigInfo{})
	m.RegisterPlatformState(nil)
	m.RecordKnowledgeChange(ctx, "datahub", "applied")
	m.RecordSearchResults(ctx, 1)
	m.RecordArchiveExtraction(ctx, 1, 1)
	m.RecordArchiveRefusal(ctx, "corrupt")
	_, op := m.StartOp(ctx, "search.query")
	op.End(ctx, errors.New("x"))
	var tr *Tracer
	if tr.SamplerRatio() != 0 {
		t.Error("a nil tracer reports a sampler ratio")
	}
}

// TestStartOp_SpanUnderCall shows an operation opens a span of its name under
// the trace the context carries, and records an error on it.
func TestStartOp_SpanUnderCall(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tr := NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)), TracingConfig{Enabled: true, SamplerArg: 1})
	t.Cleanup(func() { _ = tr.Shutdown(context.Background()) })
	if tr.SamplerRatio() != 1 {
		t.Errorf("SamplerRatio = %v", tr.SamplerRatio())
	}

	ctx, parent := tr.Start(context.Background(), "tool_call")
	var m *Metrics
	_, op := m.StartOp(ctx, "knowledge.apply")
	op.End(ctx, errors.New("datahub refused"))
	parent.End()

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	child := spans[0]
	if child.Name() != "knowledge.apply" {
		t.Errorf("span name = %q", child.Name())
	}
	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("the operation span is not a child of the call")
	}
	if child.Status().Description != StatusUpstreamErr {
		t.Errorf("status = %+v", child.Status())
	}
}

func TestLabelSafe(t *testing.T) {
	for in, want := range map[string]string{
		"trino": "trino", " oidc ": "oidc", "a://b": "", "user@host": "", "host:8080": "",
		"a/b": "", "k=v": "", "has space": "",
	} {
		if got := labelSafe(in); got != want {
			t.Errorf("labelSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

// labelRe matches one label="value" pair on a scrape line.
var labelRe = regexp.MustCompile(`[a-z_]+="([^"]*)"`)

func labelValues(line string) []string {
	matches := labelRe.FindAllStringSubmatch(line, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func seriesLine(t *testing.T, body, prefix string) string {
	t.Helper()
	for l := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no %s series in the scrape", prefix)
	return ""
}

func grepLines(body, sub string) string {
	var out []string
	for l := range strings.SplitSeq(body, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
