package sitehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
)

const hostsRoute = "/api/v1/admin/sites/hosts"

// domainStore answers Domains; nothing else in these tests reads a store.
type domainStore struct {
	site.Store
	domains []site.Domain
	err     error
}

func (s *domainStore) Domains(context.Context) ([]site.Domain, error) { return s.domains, s.err }

func requestHosts(t *testing.T, svc *sitesvc.Service) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, hostsRoute, nil)
	rec := httptest.NewRecorder()
	hosts(svc)(rec, req)
	return rec
}

func TestLayer_ANilOnePassesEverythingThrough(t *testing.T) {
	var layer *Layer
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

	rec := httptest.NewRecorder()
	layer.Wrap(next).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://any.example/", nil))
	assert.Equal(t, http.StatusTeapot, rec.Code)

	stop := layer.Start(context.Background())
	require.NotNil(t, stop)
	assert.NotPanics(t, stop)
}

func TestBuild_NoPlatformIsNoLayer(t *testing.T) {
	layer, err := Build(http.NewServeMux(), nil, nil)
	require.NoError(t, err)
	assert.Nil(t, layer)
}

func TestLayer_WrapRoutesAServedHostToTheGateway(t *testing.T) {
	gateway := sitegw.New(sitegw.Deps{
		Store: gatewayStore{}, Config: siteconfig.Config{PagesDomain: "pages.example.com"}, PortalHost: "portal.example.com",
	})
	layer := &Layer{gateway: gateway}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := layer.Wrap(next)

	served := httptest.NewRecorder()
	handler.ServeHTTP(served, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://pages.example.com/", nil))
	assert.Equal(t, http.StatusNotFound, served.Code, "the bare pages domain is the gateway's, and it answers 404")

	portal := httptest.NewRecorder()
	handler.ServeHTTP(portal, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://portal.example.com/", nil))
	assert.Equal(t, http.StatusTeapot, portal.Code)
}

// gatewayStore serves nothing and verifies nothing.
type gatewayStore struct{}

func (gatewayStore) RouteHost(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (gatewayStore) RoutePreview(context.Context, string, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (gatewayStore) Files(context.Context, string) ([]site.File, error) { return nil, nil }

func (gatewayStore) VerifiedHosts(context.Context) ([]string, error) { return nil, nil }

func TestLayer_StartReturnsWhatStopsTheSweep(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	layer := &Layer{runtime: &sitewire.Runtime{Store: sitestore.New(db)}}
	stop := layer.Start(context.Background())
	require.NotNil(t, stop)

	stop()
	select {
	case <-layer.done:
	default:
		assert.Fail(t, "the sweep loop was still running after stop returned")
	}
}

func TestHosts_WritesTheReport(t *testing.T) {
	now := time.Now()
	svc := &sitesvc.Service{
		Store: &domainStore{domains: []site.Domain{
			{Hostname: "verified.example", VerifiedAt: &now},
			{Hostname: "waiting.example"},
		}},
		Config: siteconfig.Config{PagesDomain: "pages.example.com"},
	}

	rec := requestHosts(t, svc)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var report sitesvc.HostReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	assert.Equal(t, []string{"verified.example"}, report.Hostnames)
	assert.Equal(t, []string{"waiting.example"}, report.Unverified)
	assert.Equal(t, "*.pages.example.com", report.PagesWildcard)
	assert.Contains(t, report.CertManager, `"verified.example"`)
	assert.Contains(t, report.CertManager, `"*.pages.example.com"`)
}

func TestHosts_AStoreFailureAnswers500(t *testing.T) {
	svc := &sitesvc.Service{Store: &domainStore{err: errors.New("connection to 10.0.0.5 refused")}}
	rec := requestHosts(t, svc)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be read")
	assert.NotContains(t, rec.Body.String(), "10.0.0.5", "what the store said stays in the log")
}
