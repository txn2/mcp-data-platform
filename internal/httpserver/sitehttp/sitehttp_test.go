package sitehttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
)

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
	layer, err := Build(http.NewServeMux(), nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, layer)
	assert.False(t, serving(nil))
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

// gatewayStore serves nothing and maps nothing.
type gatewayStore struct{}

func (gatewayStore) RouteHost(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (gatewayStore) RouteOwn(context.Context, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}

func (gatewayStore) Files(context.Context, string) ([]site.File, error) { return nil, nil }

func (gatewayStore) Hosts(context.Context) ([]string, error) { return nil, nil }

func TestLayer_StartReturnsWhatStopsTheLoops(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	layer := &Layer{runtime: &sitewire.Runtime{
		Store:    sitestore.New(db),
		Settings: settingsjson.New[siteconfig.Settings](db, siteconfig.SettingsSection),
		Service:  &sitesvc.Service{},
		Live:     siteconfig.NewLive(siteconfig.Config{}),
	}}
	stop := layer.Start(context.Background())
	require.NotNil(t, stop)

	stop()
	select {
	case <-layer.done:
	default:
		assert.Fail(t, "the loops were still running after stop returned")
	}
}

func (gatewayStore) RouteEnv(context.Context, string, string) (*site.Route, error) {
	return nil, site.ErrNoDomain
}
