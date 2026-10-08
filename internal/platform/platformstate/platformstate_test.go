package platformstate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/connstate"
	"github.com/txn2/mcp-data-platform/internal/opsobs"
	"github.com/txn2/mcp-data-platform/internal/platform/thumbworker"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// listingToolkit lists connections and nothing else.
type listingToolkit struct {
	kind  string
	conns []string
}

func (p *listingToolkit) Kind() string                        { return p.kind }
func (p *listingToolkit) Name() string                        { return p.kind + "-tk" }
func (*listingToolkit) Connection() string                    { return "" }
func (*listingToolkit) Tools() []string                       { return nil }
func (*listingToolkit) RegisterTools(*mcp.Server)             {}
func (*listingToolkit) SetSemanticProvider(semantic.Provider) {}
func (*listingToolkit) SetQueryProvider(query.Provider)       {}
func (*listingToolkit) Close() error                          { return nil }
func (p *listingToolkit) ListConnections() []toolkit.ConnectionDetail {
	out := make([]toolkit.ConnectionDetail, 0, len(p.conns))
	for _, c := range p.conns {
		out = append(out, toolkit.ConnectionDetail{Name: c})
	}
	return out
}

func registryOf(t *testing.T, tks ...registry.Toolkit) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()
	for _, tk := range tks {
		if err := reg.Register(tk); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func enabledMetrics(t *testing.T) (m *observability.Metrics, scrape func() string) {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	scrape = func() string {
		rec := httptest.NewRecorder()
		m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}
	return m, scrape
}

// TestSampler_ReportsDependenciesAndConnections holds the acceptance
// sentence: a connection whose upstream is down reports
// mcp_platform_connections{state="unreachable"}, beside the healthy, the
// credential-refused and the never-called ones, and every dependency's probe.
// The connection states are the ones the last calls recorded; nothing probes.
func TestSampler_ReportsDependenciesAndConnections(t *testing.T) {
	m, scrape := enabledMetrics(t)
	trino := &listingToolkit{kind: "trino", conns: []string{"up", "down"}}
	api := &listingToolkit{kind: "api", conns: []string{"crm"}}
	plain := &listingToolkit{kind: "s3", conns: []string{"lake"}}
	connstate.Observe("trino", "up", connstate.Healthy)
	connstate.Observe("trino", "down", connstate.Unreachable)
	connstate.Observe("api", "crm", connstate.AuthFailed)
	connstate.Observe("api", "not-listed", connstate.Healthy)
	personas := persona.NewRegistry()
	_ = personas.Register(&persona.Persona{Name: "analyst"})

	s := Start(m, Deps{
		Dependencies: map[string]Pinger{
			opsobs.DependencyIDP:      PingFunc(func(context.Context) error { return nil }),
			opsobs.DependencyRenderer: PingFunc(func(context.Context) error { return errors.New("no renderer") }),
			opsobs.DependencyQuery:    nil,
		},
		Toolkits: registryOf(t, trino, api, plain), Personas: personas,
	})
	s.Wait(context.Background())

	body := scrape()
	for _, want := range []string{
		`dependency_up{dependency="idp"} 1`,
		`dependency_up{dependency="renderer"} 0`,
		`mcp_platform_connections{kind="trino",state="healthy"} 1`,
		`mcp_platform_connections{kind="trino",state="unreachable"} 1`,
		`mcp_platform_connections{kind="api",state="auth_failed"} 1`,
		`mcp_platform_connections{kind="s3",state="configured"} 1`,
		`mcp_platform_personas 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	if strings.Contains(body, `dependency="query"`) {
		t.Error("an unconfigured dependency was reported")
	}
}

// TestSampler_RefreshesWhenStale shows a scrape answers from the last result
// and starts one refresh once that result is older than the interval.
func TestSampler_RefreshesWhenStale(t *testing.T) {
	up := true
	s := New(Deps{Dependencies: map[string]Pinger{"idp": PingFunc(func(context.Context) error {
		if up {
			return nil
		}
		return errors.New("down")
	})}, Interval: time.Minute, ProbeTimeout: time.Second})
	now := time.Unix(1_000, 0)
	s.now = func() time.Time { return now }

	s.Refresh(context.Background())
	if got := s.Sample(context.Background()).Dependencies; len(got) != 1 || !got[0].Up {
		t.Fatalf("first result = %+v", got)
	}
	up = false
	if got := s.Sample(context.Background()).Dependencies; !got[0].Up {
		t.Fatal("a fresh result was re-probed")
	}
	now = now.Add(2 * time.Minute)
	stale := s.Sample(context.Background()).Dependencies
	if !stale[0].Up {
		t.Error("a stale scrape waited for the probe instead of answering from the last result")
	}
	s.Wait(context.Background())
	if got := s.Sample(context.Background()).Dependencies; got[0].Up {
		t.Errorf("the background refresh did not land: %+v", got)
	}
}

// TestSampler_IndexAges reads the newest successful index pass per kind.
func TestSampler_IndexAges(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectPing()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`SELECT source_kind`).WillReturnRows(
		sqlmock.NewRows([]string{"source_kind", "age"}).AddRow("tools", 120.0).AddRow("prompts", 5.5))

	s := New(Deps{DB: db})
	s.Refresh(context.Background())
	out := s.Sample(context.Background())
	if len(out.IndexAges) != 2 || out.IndexAges[0].Kind != "prompts" || out.IndexAges[1].Age != 2*time.Minute {
		t.Errorf("index ages = %+v", out.IndexAges)
	}
	if len(out.Dependencies) != 1 || out.Dependencies[0].Dependency != opsobs.DependencyDatabase || !out.Dependencies[0].Up {
		t.Errorf("database dependency = %+v", out.Dependencies)
	}
	if out.PersonasKnown {
		t.Error("a persona count without a registry")
	}
}

func TestIndexAges_ReadFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`SELECT source_kind`).WillReturnError(errors.New("relation missing"))
	if got := indexAges(context.Background(), db); got != nil {
		t.Errorf("ages on a failed read = %+v", got)
	}
	if got := indexAges(context.Background(), nil); got != nil {
		t.Errorf("ages without a database = %+v", got)
	}
}

func TestStart_MetricsOff(t *testing.T) {
	s := Start(nil, Deps{})
	s.Wait(context.Background())
	if got := s.Sample(context.Background()); len(got.Dependencies) != 0 {
		t.Errorf("probed with metrics off: %+v", got)
	}
}

// pingAdapter is a provider that answers Ping.
type pingAdapter struct {
	semantic.Provider
	err error
}

func (pingAdapter) Name() string                 { return "datahub" }
func (p pingAdapter) Ping(context.Context) error { return p.err }

// wrapped exposes its provider the way the semantic cache does.
type wrapped struct {
	semantic.Provider
	inner semantic.Provider
}

func (wrapped) Name() string                { return "cache" }
func (w wrapped) Unwrap() semantic.Provider { return w.inner }

func TestProviderPinger(t *testing.T) {
	if providerPinger(semantic.NewNoopProvider()) != nil {
		t.Error("a noop provider is a dependency")
	}
	if providerPinger(query.NewNoopProvider()) != nil {
		t.Error("a noop query provider is a dependency")
	}
	if providerPinger(nil) != nil {
		t.Error("nil is a dependency")
	}
	if p := providerPinger(wrapped{inner: pingAdapter{}}); p == nil {
		t.Error("the pinger under a cache wrapper was not found")
	}
	if providerPinger(wrapped{inner: wrapped{inner: wrapped{inner: wrapped{inner: wrapped{inner: pingAdapter{}}}}}}) != nil {
		t.Error("the wrapper walk is unbounded")
	}
}

// listerFake answers ListDirectory.
type listerFake struct{ err error }

func (l listerFake) ListDirectory(context.Context, string, string) ([]s3adapter.ObjectEntry, bool, error) {
	return nil, false, l.err
}

// TestWire_ConfigInfoAndDependencies records config info from the sources and
// probes the dependencies they configure.
func TestWire_ConfigInfoAndDependencies(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"issuer":"x","jwks_uri":"https://idp/certs"}`)
	}))
	defer idp.Close()
	m, scrape := enabledMetrics(t)
	off := false
	s := Wire(m, Sources{
		Semantic: wrapped{inner: pingAdapter{}},
		Query:    query.NewNoopProvider(),
		Objects:  listerFake{err: errors.New("access denied")}, Bucket: "portal-assets",
		Auth:       Auth{OIDC: true, APIKeys: true, Browser: true, Issuer: idp.URL},
		Thumbnails: thumbworker.Config{Enabled: &off},
		Toolkits:   registryOf(t, &listingToolkit{kind: "trino"}),
	})
	s.Wait(context.Background())
	body := scrape()
	for _, want := range []string{
		`auth_methods="api_key,browser,oidc"`,
		`toolkit_kinds="trino"`,
		`tracing="false"`,
		`dependency_up{dependency="semantic"} 1`,
		`dependency_up{dependency="object_storage"} 0`,
		`dependency_up{dependency="idp"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
	for _, absent := range []string{`dependency="query"`, `dependency="renderer"`} {
		if strings.Contains(body, absent) {
			t.Errorf("%s reported", absent)
		}
	}
}

func TestIdPProbe(t *testing.T) {
	noJWKS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"issuer":"x"}`)
	}))
	defer noJWKS.Close()
	p := idpProbe{issuer: noJWKS.URL, client: noJWKS.Client()}
	if err := p.Ping(context.Background()); !errors.Is(err, errNoJWKS) {
		t.Errorf("err = %v", err)
	}
	if err := (idpProbe{issuer: "http://127.0.0.1:1", client: http.DefaultClient}).Ping(context.Background()); err == nil {
		t.Error("an unreachable issuer answered")
	}
}

func TestDependencies_Renderer(t *testing.T) {
	deps := dependencies(Sources{Thumbnails: thumbworker.Config{}})
	if _, ok := deps[opsobs.DependencyRenderer]; !ok {
		t.Error("the default renderer is not probed")
	}
	if _, ok := dependencies(Sources{Objects: listerFake{}})[opsobs.DependencyObjectStorage]; ok {
		t.Error("object storage probed with no bucket")
	}
}

// Wait blocks until the refresh in flight, if any, finishes or ctx ends.
func (s *Sampler) Wait(ctx context.Context) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}
