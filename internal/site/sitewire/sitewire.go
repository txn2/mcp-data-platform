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
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitecrawl"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/internal/site/siteverify"
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
	Approvals    *approval.Service
	Sources      *scriptsrc.Reader
}

// Runtime is the assembled feature.
type Runtime struct {
	Service *sitesvc.Service
	Store   *sitestore.Store
	Config  siteconfig.Config
	Apps    *appstore.Store
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
	svc := &sitesvc.Service{
		Store: store, Bucket: d.Bucket, Config: d.Config, Apps: apps,
		PortalHost: site.NormalizeHost(portal.Hostname()), CookieDomain: d.CookieDomain,
		PublicScheme: scheme(portal), PublicPort: portal.Port(),
	}
	if d.Objects != nil && d.Config.IsEnabled() {
		svc.Objects = d.Objects
	}
	if d.Approvals != nil {
		svc.Approvals = d.Approvals
	}
	verifier, err := siteverify.New(d.Config.Verify)
	if err != nil {
		return nil, fmt.Errorf("sites: %w", err)
	}
	svc.Verifier = verifier
	tool := &sitetool.Tool{Service: svc, PortalURL: d.PortalURL}
	if d.Sources != nil {
		tool.Sources = sources{reader: d.Sources, limits: d.Config.Limits()}
	}
	crawler, err := sitecrawl.New(d.Config.Crawl)
	if err != nil {
		return nil, fmt.Errorf("sites: %w", err)
	}
	tool.Crawler = crawler
	tool.Register(server)
	return &Runtime{Service: svc, Store: store, Config: d.Config, Apps: apps}, nil
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
	if rt.Config.PagesDomain == "" {
		return errors.New("this deployment serves no browser pages: it has no sites.pages_domain")
	}
	a, err := rt.Apps.Owning(ctx, "script", scriptID)
	if err != nil {
		return fmt.Errorf("a page belongs to an application, and this script is a part of none: add it with manage_application add_part: %w", epbinding.ErrNotAllowed)
	}
	if strings.Contains(a.Name, site.PreviewSeparator) {
		return fmt.Errorf("application %q cannot serve pages: a name holding %q is read as a site preview", a.Name, site.PreviewSeparator)
	}
	return nil
}

// SiteOrigins are the browser origins of the sites an application holds: its
// site parts' verified hostnames and previews, which its endpoints answer
// without being listed in allowed_origins.
func (rt *Runtime) SiteOrigins(ctx context.Context, applicationID string) []string {
	if applicationID == "" {
		return nil
	}
	hosts, err := rt.Store.ApplicationHosts(ctx, applicationID, rt.Config.PagesDomain)
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
	limits siteconfig.Config
}

// Archive reads a zip or gzipped tar managed resource's files.
func (s sources) Archive(ctx context.Context, ref string) ([]sitesvc.InputFile, error) {
	claims := resourcewrite.CallerClaims(ctx)
	res, err := s.reader.Resolve(ctx, ref, claims)
	if err != nil {
		return nil, &sitesvc.RefusedError{Msg: err.Error()}
	}
	body, err := s.reader.Bytes(ctx, res, s.limits.MaxReleaseBytes)
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
		MaxMemberBytes: s.limits.MaxFileBytes, MaxTotalBytes: s.limits.MaxReleaseBytes, MaxMembers: s.limits.MaxFiles + 1,
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
		if total > s.limits.MaxReleaseBytes {
			return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("the folder holds more than the %d bytes sites.max_release_bytes allows", s.limits.MaxReleaseBytes)}
		}
		body, err := s.reader.Bytes(ctx, f.Resource, s.limits.MaxFileBytes)
		if err != nil {
			return nil, &sitesvc.RefusedError{Msg: err.Error()}
		}
		files = append(files, sitesvc.InputFile{Path: f.Path, Body: body})
	}
	return files, nil
}
