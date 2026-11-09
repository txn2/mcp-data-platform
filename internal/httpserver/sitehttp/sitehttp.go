// Package sitehttp assembles the serving layer for sites and browser pages
// (#2517, #2518) over the platform's mux: the Host-routing gateway the
// listener is wrapped in, the portal routes that hand a signed-in person to a
// page origin or a site's own address, and the REST routes of the portal's
// Sites section and its administration (#2559).
package sitehttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/site/sitegw"
	"github.com/txn2/mcp-data-platform/internal/site/sitewire"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

const (
	sweepEvery = time.Hour
	// refreshEvery is how soon a replica reads a setting another one saved.
	refreshEvery = 5 * time.Second
)

// Layer is the assembled serving layer; a nil one passes everything through.
type Layer struct {
	gateway *sitegw.Gateway
	runtime *sitewire.Runtime
	stop    chan struct{}
	done    chan struct{}
}

// Build assembles the layer, nil where the deployment has no sites.
func Build(mux *http.ServeMux, p *platform.Platform, adminAuth func() func(http.Handler) http.Handler, author func(*http.Request) string) (*Layer, error) {
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
	mux.HandleFunc("GET /sites/handoff", deps.Access.Handoff)
	gateway := sitegw.New(deps)
	rt.Service.Invalidate = gateway.Invalidate
	layer := &Layer{gateway: gateway, runtime: rt}
	if p.Config().Admin.IsEnabled() {
		(&adminAPI{rt: rt, author: author}).mount(mux, adminAuth())
	}
	return layer, nil
}

// serving reports whether the deployment serves sites at all.
func serving(p *platform.Platform) bool {
	return p != nil && p.Endpoints() != nil && p.Endpoints().Sites != nil && p.Endpoints().Sites.Serving()
}

// gatewayDeps is what the gateway serves from, Pages set where the deployment
// has an endpoint handler: the pages domain can be saved at any time.
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
		Store: rt.Store, Objects: objects, Bucket: p.Config().Portal.S3Bucket, Config: rt.Config, Live: rt.Live, PortalHost: rt.Service.PortalHost,
		Access: accessOver(p, rt, portal),
	}
	if p.Endpoints().Handler != nil {
		deps.Pages = pagesOver(p, rt, portal)
	}
	return deps, nil
}

func pagesOver(p *platform.Platform, rt *sitewire.Runtime, portal *url.URL) *sitegw.Pages {
	e := p.Endpoints()
	return &sitegw.Pages{
		Apps: rt.Apps, Assets: p.PortalAssetStore(), Versions: p.PortalVersionStore(), Server: e.Handler,
		Authenticate: e.PortalCaller, RouteHost: rt.Store.RouteHost, UseHandoff: rt.Store.UseHandoff,
		FormKey: e.Runner.FormKey, PortalURL: p.Config().Portal.PublicBaseURL, PagesDomain: func() string { return rt.Live.Get().PagesDomain },
		Secure: portal.Scheme == "https", PublicPort: portal.Port(),
	}
}

// accessOver is what guards a site's own address. Its session reads the portal's
// browser cookie alone, so an API key signs nobody in, and it has no persona
// gate: an external person with a grant and no Sites area gets through to the
// check made on every request.
func accessOver(p *platform.Platform, rt *sitewire.Runtime, portal *url.URL) *sitegw.Access {
	reg, admin := p.PersonaRegistry(), p.Config().Admin.Persona
	external := func(name string) bool {
		found, ok := reg.Get(name)
		return ok && found.External
	}
	a := &sitegw.Access{
		Store: rt.Store, FormKey: p.Endpoints().Runner.FormKey, PortalURL: p.Config().Portal.PublicBaseURL,
		PagesDomain: func() string { return rt.Live.Get().PagesDomain },
		IsAdmin:     func(name string) bool { return reg.IsAdminName(name, admin) },
		IsExternal:  external, Secure: portal.Scheme == "https", PublicPort: portal.Port(),
		Session: func(*http.Request) (*sitegw.SiteViewer, error) { return nil, nil }, //nolint:nilnil // no browser sign-in: nobody is signed in
	}
	if ba := p.BrowserSessionAuth(); ba != nil {
		a.Session = func(r *http.Request) (*sitegw.SiteViewer, error) {
			info, err := ba.AuthenticateHTTP(r)
			if err != nil || info == nil {
				return nil, err //nolint:wrapcheck // nobody is signed in
			}
			v := &sitegw.SiteViewer{UserID: info.UserID, Email: info.Email}
			if found, ok := reg.GetForRoles(info.Roles); ok {
				v.Persona = found.Name
			}
			verified, stated := info.Claims[browsersession.ClaimEmailVerified].(bool)
			v.EmailUnverified = stated && !verified
			return v, nil
		}
	}
	return a
}

// Wrap sends a served hostname to the gateway.
func (l *Layer) Wrap(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return l.gateway.Wrap(next)
}

// Start begins the handoff sweep and the reading of saved settings, and
// returns what stops them.
func (l *Layer) Start(ctx context.Context) (stop func()) {
	if l == nil {
		return func() {}
	}
	l.stop, l.done = make(chan struct{}), make(chan struct{})
	var loops sync.WaitGroup
	loops.Go(func() {
		bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameSiteHandoffSweep, Every: sweepEvery, Stop: l.stop, Body: func(ctx context.Context) error {
			return l.runtime.Store.ExpireHandoffs(ctx, time.Now()) //nolint:wrapcheck // the loop records it
		}})
	})
	loops.Go(func() {
		bgloop.Run(ctx, bgloop.Loop{Name: bgloop.NameSiteSettingsRefresh, Every: refreshEvery, Stop: l.stop, Body: l.runtime.Refresh})
	})
	go func() {
		loops.Wait()
		close(l.done)
	}()
	return l.shutdown
}

func (l *Layer) shutdown() {
	if l.stop != nil {
		close(l.stop)
		<-l.done
	}
}
