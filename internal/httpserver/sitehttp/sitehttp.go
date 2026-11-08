// Package sitehttp assembles the serving layer for sites and browser pages
// (#2517, #2518) over the platform's mux: the Host-routing gateway the
// listener is wrapped in, the portal route that hands a signed-in person to a
// page origin, the administrators' report of the hostnames the ingress needs,
// and the optional ACME listener.
package sitehttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/site/sitecert"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

const (
	sweepEvery  = time.Hour
	stopTimeout = 10 * time.Second
)

// Layer is the assembled serving layer; a nil one passes everything through.
type Layer struct {
	gateway  *sitegw.Gateway
	runtime  *sitewire.Runtime
	listener *sitecert.Listener
	stop     chan struct{}
	done     chan struct{}
}

// Build assembles the layer, nil where the deployment has no sites.
func Build(mux *http.ServeMux, p *platform.Platform, adminAuth func() func(http.Handler) http.Handler) (*Layer, error) {
	if !serving(p) {
		return nil, nil //nolint:nilnil // a deployment with no sites
	}
	rt := p.Endpoints().Sites
	deps, err := gatewayDeps(p, rt)
	if err != nil {
		return nil, err
	}
	if deps.Pages != nil {
		mux.HandleFunc("GET /pages/handoff", deps.Pages.Handoff)
	}
	gateway := sitegw.New(deps)
	rt.Service.Invalidate = gateway.Invalidate
	layer := &Layer{gateway: gateway, runtime: rt}
	if p.Config().Admin.IsEnabled() {
		mux.Handle("GET /api/v1/admin/sites/hosts", adminAuth()(hosts(rt.Service)))
	}
	if rt.Config.TLS.ACME.Enabled {
		if layer.listener, err = acmeListener(p, rt, gateway); err != nil {
			return nil, err
		}
	}
	return layer, nil
}

// serving reports whether the deployment serves sites at all.
func serving(p *platform.Platform) bool {
	return p != nil && p.Endpoints() != nil && p.Endpoints().Sites != nil && p.Endpoints().Sites.Serving()
}

// gatewayDeps is what the gateway serves from, Pages set where the deployment
// has a pages domain and an endpoint handler.
func gatewayDeps(p *platform.Platform, rt *sitewire.Runtime) (sitegw.Deps, error) {
	objects, ok := p.PortalS3Client().(sitegw.Objects)
	if !ok {
		return sitegw.Deps{}, errors.New("sites: the portal's object client cannot read by range")
	}
	portal, err := url.Parse(p.Config().Portal.PublicBaseURL)
	if err != nil {
		return sitegw.Deps{}, errors.New("sites: portal.public_base_url is not a URL")
	}
	deps := sitegw.Deps{
		Store: rt.Store, Objects: objects, Bucket: p.Config().Portal.S3Bucket, Config: rt.Config, PortalHost: rt.Service.PortalHost,
	}
	if rt.Config.PagesDomain != "" && p.Endpoints().Handler != nil {
		deps.Pages = pagesOver(p, rt, portal)
	}
	return deps, nil
}

func pagesOver(p *platform.Platform, rt *sitewire.Runtime, portal *url.URL) *sitegw.Pages {
	e := p.Endpoints()
	return &sitegw.Pages{
		Apps: rt.Apps, Assets: p.PortalAssetStore(), Versions: p.PortalVersionStore(), Server: e.Handler,
		Authenticate: e.PortalCaller, RouteHost: rt.Store.RouteHost, UseHandoff: rt.Store.UseHandoff,
		FormKey: e.Runner.FormKey, PortalURL: p.Config().Portal.PublicBaseURL, PagesDomain: rt.Config.PagesDomain,
		Secure: portal.Scheme == "https", PublicPort: portal.Port(),
	}
}

func acmeListener(p *platform.Platform, rt *sitewire.Runtime, gateway *sitegw.Gateway) (*sitecert.Listener, error) {
	sealer := p.RestEncryptor()
	if sealer == nil || !sealer.Enabled() {
		return nil, errors.New("sites.tls.acme needs ENCRYPTION_KEY: certificates and their keys are sealed at rest")
	}
	return sitecert.New(rt.Config.TLS.ACME, p.DB(), sealer, gateway.Exists, gateway.Wrap(http.NotFoundHandler())) //nolint:wrapcheck // the configuration's own sentence
}

// Wrap sends a served hostname to the gateway.
func (l *Layer) Wrap(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return l.gateway.Wrap(next)
}

// Start begins the handoff sweep and the ACME listener, and returns what
// stops them.
func (l *Layer) Start(ctx context.Context) (stop func()) {
	if l == nil {
		return func() {}
	}
	l.stop, l.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(l.done)
		bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameSiteHandoffSweep, Every: sweepEvery, Stop: l.stop, Body: func(ctx context.Context) error {
			return l.runtime.Store.ExpireHandoffs(ctx, time.Now()) //nolint:wrapcheck // the loop records it
		}})
	}()
	if l.listener != nil {
		go func() {
			if err := l.listener.Start(); err != nil {
				slog.ErrorContext(ctx, "sites: the TLS listener stopped", "error", logsan.SanitizeForLog(err.Error()))
			}
		}()
	}
	return l.shutdown
}

func (l *Layer) shutdown() {
	if l.stop != nil {
		close(l.stop)
		<-l.done
	}
	if l.listener != nil {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := l.listener.Stop(ctx); err != nil {
			slog.WarnContext(ctx, "sites: stopping the TLS listener", "error", logsan.SanitizeForLog(err.Error()))
		}
	}
}

// hosts godoc
//
//	@Summary      The hostnames the ingress needs for sites
//	@Description  Lists the verified hostnames sites and application pages answer on, those awaiting verification, the wildcard the pages domain needs, and a cert-manager Certificate covering them.
//	@Tags         Sites
//	@Produce      json
//	@Success      200  {object}  sitesvc.HostReport
//	@Failure      401  {object}  httpjson.ProblemDetail
//	@Security     ApiKeyAuth
//	@Security     BearerAuth
//	@Router       /admin/sites/hosts [get]
func hosts(svc *sitesvc.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := svc.Hosts(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "sites: reading the hostnames failed", "error", logsan.SanitizeForLog(err.Error()))
			http.Error(w, "the hostnames could not be read", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = wirejson.Encode(w, rep)
	}
}
