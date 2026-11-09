// Package sitewire assembles sites (#2518) from the stores the platform holds
// and registers manage_site. The serving layer is built later, once the
// endpoint routes exist, by internal/httpserver/sitehttp.
package sitewire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Deps is what sites are assembled from.
type Deps struct {
	DB        *sql.DB
	Objects   sitesvc.Objects
	Bucket    string
	Config    siteconfig.Config
	PortalURL string
	// CookieDomain is the browser session cookie's Domain, empty for none.
	CookieDomain string
	// ReservedURLs are the other addresses the platform answers on itself; a
	// domain may not claim their hostnames.
	ReservedURLs []string
	Approvals    *approval.Service
	Sources      *scriptsrc.Reader
}

// Runtime is the assembled feature.
type Runtime struct {
	Service *sitesvc.Service
	Store   *sitestore.Store
	Config  siteconfig.Config
	Apps    *appstore.Store
	// Live is the configuration in force: the file's until a saved section
	// replaces it.
	Live *siteconfig.Live
	// Settings is the section an administrator saves.
	Settings *settingsjson.Section[siteconfig.Settings]
	// Sources reads what a publish names by reference, and Crawler copies a
	// site; nil where the deployment cannot.
	Sources sitetool.Sources
	Crawler sitetool.Crawler
	// PortalURL and CookieDomain are what a saved pages domain is checked against.
	PortalURL    string
	CookieDomain string
}

// Refresh reads the saved settings into the configuration in force. A
// deployment that has saved none keeps the file's values.
func (rt *Runtime) Refresh(ctx context.Context) error {
	stored, err := rt.Settings.Get(ctx)
	if errors.Is(err, settingsjson.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the sites settings: %w", err)
	}
	next := rt.Config.With(stored.Value)
	if rt.Live.Get().PagesDomain != next.PagesDomain && rt.Service.Invalidate != nil {
		defer rt.Service.Invalidate("")
	}
	rt.Live.Set(next)
	return nil
}

// Save checks and stores the settings and applies them on this replica at once;
// the others read them within their refresh interval.
func (rt *Runtime) Save(ctx context.Context, s siteconfig.Settings, by string) error {
	next := rt.Config.With(s)
	if err := next.Check(rt.PortalURL, rt.CookieDomain); err != nil {
		return &sitesvc.RefusedError{Msg: err.Error()}
	}
	if err := rt.Settings.Set(ctx, s, by); err != nil {
		return fmt.Errorf("saving the sites settings: %w", err)
	}
	return rt.Refresh(ctx)
}

// Serving reports whether sites run here: the section is on, and there is a
// bucket to hold releases.
func (rt *Runtime) Serving() bool {
	return rt.Config.IsEnabled() && rt.Service.Objects != nil && rt.Service.Bucket != ""
}

// Register assembles the service and registers manage_site wherever there is
// a database, so a deployment that cannot serve sites says so when asked; nil
// with no database.
func Register(server *mcp.Server, d Deps) (*Runtime, error) {
	if d.DB == nil {
		return nil, nil //nolint:nilnil // a deployment with no database
	}
	portal := parsePortal(d.PortalURL)
	if err := d.Config.Validate(portal.Hostname(), d.CookieDomain); err != nil {
		return nil, err //nolint:wrapcheck // the configuration's own sentence
	}
	store := sitestore.New(d.DB)
	apps := appstore.New(d.DB)
	live := siteconfig.NewLive(d.Config)
	svc := &sitesvc.Service{
		Store: store, Bucket: d.Bucket, Config: d.Config, Live: live, Apps: apps,
		PortalHost: site.NormalizeHost(portal.Hostname()), ReservedHosts: append(reservedHosts(d.ReservedURLs), d.Config.ReservedHostnames...), CookieDomain: d.CookieDomain,
		PublicScheme: scheme(portal), PublicPort: portal.Port(),
	}
	if d.Objects != nil && d.Config.IsEnabled() {
		svc.Objects = d.Objects
	}
	if d.Approvals != nil {
		svc.Approvals = d.Approvals
	}
	tool := &sitetool.Tool{Service: svc, PortalURL: d.PortalURL}
	if d.Sources != nil {
		tool.Sources = sources{reader: d.Sources, live: live}
	}
	crawler, err := sitecrawl.New(d.Config.Crawl)
	if err != nil {
		return nil, fmt.Errorf("sites: %w", err)
	}
	tool.Crawler = crawler
	tool.Register(server)
	rt := &Runtime{
		Service: svc, Store: store, Config: d.Config, Apps: apps, Live: live,
		Settings: settingsjson.New[siteconfig.Settings](d.DB, siteconfig.SettingsSection),
		Sources:  tool.Sources, Crawler: crawler, PortalURL: d.PortalURL, CookieDomain: d.CookieDomain,
	}
	err = rt.Refresh(context.Background())
	return rt, err
}

// reservedHosts are the hostnames of the platform's other addresses.
func reservedHosts(urls []string) []string {
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Hostname() != "" {
			out = append(out, site.NormalizeHost(u.Hostname()))
		}
	}
	return out
}

func parsePortal(raw string) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return &url.URL{Scheme: "https"}
	}
	return u
}

func scheme(u *url.URL) string {
	if u.Scheme == "" {
		return "https"
	}
	return u.Scheme
}

// PageAllowed reports why a script's endpoint cannot be a browser page.
func (rt *Runtime) PageAllowed(ctx context.Context, scriptID string) error {
	if rt.Live.Get().PagesDomain == "" {
		return errors.New("this deployment serves no browser pages: it has no pages domain")
	}
	_, err := rt.Apps.Owning(ctx, "script", scriptID)
	if err != nil {
		return fmt.Errorf("a page belongs to an application, and this script is a part of none: add it with manage_application add_part: %w", epbinding.ErrNotAllowed)
	}
	return nil
}

// PageAddress is where a script's browser page answers: its application's page
// origin, "" for a script in no application or with no pages domain.
func (rt *Runtime) PageAddress(ctx context.Context, scriptID string) string {
	a, err := rt.Apps.Owning(ctx, "script", scriptID)
	if err != nil {
		return ""
	}
	return rt.Service.PageOrigin(a.Name)
}

// ApplicationAddresses is where an application answers: its page origin and the
// domains mapped to it.
func (rt *Runtime) ApplicationAddresses(ctx context.Context, name string) (origin string, domains []string, err error) {
	if _, err := rt.Apps.GetByName(ctx, name); err != nil {
		return "", nil, fmt.Errorf("application %q: %w", name, err)
	}
	all, err := rt.Store.Domains(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("listing domains: %w", err)
	}
	domains = []string{}
	for _, d := range all {
		if d.Application == name {
			domains = append(domains, d.Hostname)
		}
	}
	return rt.Service.PageOrigin(name), domains, nil
}

// SiteOrigins are the browser origins of the sites an application holds: its
// site parts' domains and own addresses, which its endpoints answer without
// being listed in allowed_origins.
func (rt *Runtime) SiteOrigins(ctx context.Context, applicationID string) []string {
	if applicationID == "" {
		return nil
	}
	hosts, err := rt.Store.ApplicationHosts(ctx, applicationID, rt.Live.Get().PagesDomain)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, rt.Service.Origin(h))
	}
	return out
}

// sources reads what a publish names by reference, as its caller.
type sources struct {
	reader *scriptsrc.Reader
	live   *siteconfig.Live
}

func (s sources) limits() siteconfig.Config { return s.live.Get().Limits() }

// Archive reads a zip or gzipped tar managed resource's files.
func (s sources) Archive(ctx context.Context, ref string) ([]sitesvc.InputFile, error) {
	limits := s.limits()
	claims := resourcewrite.CallerClaims(ctx)
	res, err := s.reader.Resolve(ctx, ref, claims)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	body, err := s.reader.Bytes(ctx, res, limits.MaxReleaseBytes)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	src := unarchive.Source{Name: res.Filename, Size: int64(len(body)), Fetch: func(_ context.Context, off, length int64) ([]byte, error) {
		end := min(off+length, int64(len(body)))
		if off >= end {
			return nil, nil
		}
		return body[off:end], nil
	}}
	archive, err := unarchive.Open(ctx, src, "", unarchive.Limits{
		MaxMemberBytes: limits.MaxFileBytes, MaxTotalBytes: limits.MaxReleaseBytes, MaxMembers: limits.MaxFiles + 1,
	})
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: "the archive cannot be published: " + err.Error()}
	}
	files := make([]sitesvc.InputFile, 0, len(archive.Members()))
	err = archive.Extract(ctx, func(m unarchive.Member, r io.Reader) error {
		data, rerr := io.ReadAll(r)
		if rerr != nil {
			return fmt.Errorf("reading %s: %w", m.Path(), rerr)
		}
		files = append(files, sitesvc.InputFile{Path: path.Clean(m.Path()), Body: data})
		return nil
	})
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: "the archive cannot be published: " + err.Error()}
	}
	return files, nil
}

// Folder reads a managed-resource folder's files.
func (s sources) Folder(ctx context.Context, scope, scopeID, folder string) ([]sitesvc.InputFile, error) {
	limits := s.limits()
	claims := resourcewrite.CallerClaims(ctx)
	lib := resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: claims.Sub}
	if scope != "" && scope != string(resource.ScopeUser) {
		lib = resource.ScopeFilter{Scope: resource.Scope(scope), ScopeID: scopeID}
	}
	listed, err := s.reader.Folder(ctx, lib, folder, claims)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	var total int64
	files := make([]sitesvc.InputFile, 0, len(listed))
	for _, f := range listed {
		total += f.Resource.SizeBytes
		if total > limits.MaxReleaseBytes {
			return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("the folder holds more than the %d bytes sites.max_release_bytes allows", limits.MaxReleaseBytes)}
		}
		body, err := s.reader.Bytes(ctx, f.Resource, limits.MaxFileBytes)
		if err != nil {
			return nil, &sitesvc.RefusedError{Msg: err.Error()}
		}
		files = append(files, sitesvc.InputFile{Path: f.Path, Body: body})
	}
	return files, nil
}
