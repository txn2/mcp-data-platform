package sitesvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// DomainRequest maps one hostname.
type DomainRequest struct {
	Hostname    string
	Site        string
	Environment string
	Application string
}

// AddDomain records a hostname, unverified, and says how to verify it.
func (s *Service) AddDomain(ctx context.Context, actor Actor, req DomainRequest) (*site.Domain, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	host := site.NormalizeHost(req.Hostname)
	if err := s.claimable(host); err != nil {
		return nil, err
	}
	d := &site.Domain{Hostname: host, Token: newToken(), CreatedBy: actor.Email}
	if err := s.target(ctx, d, req); err != nil {
		return nil, err
	}
	if err := s.Store.AddDomain(ctx, d); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	return s.Store.Domain(ctx, host) //nolint:wrapcheck // read by identity
}

// claimable refuses a hostname that is not a plain name, is the platform's
// own, or sits where a served page would receive the session cookie.
func (s *Service) claimable(host string) error {
	if err := site.ValidateHostname(host); err != nil {
		return refused("%s", err.Error())
	}
	switch {
	case site.SameOrUnder(host, s.Config.PagesDomain):
		return refused("%s is under the pages domain, which the platform assigns", host)
	case host == s.PortalHost:
		return refused("%s is the portal's own hostname", host)
	case site.SameOrUnder(host, strings.TrimPrefix(s.CookieDomain, ".")):
		return refused("%s is under the session cookie's domain %s, so its pages would receive the portal's session", host, s.CookieDomain)
	}
	return nil
}

func (s *Service) target(ctx context.Context, d *site.Domain, req DomainRequest) error {
	switch {
	case (req.Site == "") == (req.Application == ""):
		return refused("a hostname maps to a site and environment, or to an application")
	case req.Application != "":
		if s.Apps == nil {
			return refused("this deployment has no applications")
		}
		a, err := s.Apps.GetByName(ctx, req.Application)
		if err != nil {
			return fmt.Errorf("application %q: %w", req.Application, err)
		}
		d.ApplicationID = a.ID
		return nil
	}
	st, err := s.Find(ctx, req.Site)
	if err != nil {
		return err
	}
	if st.Environment(req.Environment) == nil {
		return noEnvironment(req.Environment)
	}
	d.SiteID, d.Environment = st.ID, req.Environment
	return nil
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "platform-site-verify=" + hex.EncodeToString(b[:])
}

// VerifyDomain runs the hostname's verification step and, when it passes,
// starts routing it.
func (s *Service) VerifyDomain(ctx context.Context, actor Actor, hostname string) (*site.Domain, error) {
	if !actor.Admin {
		return nil, errNeedsAdmin
	}
	host := site.NormalizeHost(hostname)
	d, err := s.Store.Domain(ctx, host)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Verifier == nil {
		return nil, refused("this deployment cannot verify domains")
	}
	if err := s.Verifier.Verify(ctx, host, d.Token); err != nil {
		if r, ok := errors.AsType[*RefusedError](err); ok {
			return nil, r
		}
		return nil, fmt.Errorf("verifying %s: %w", host, err)
	}
	if err := s.Store.MarkVerified(ctx, host, actor.Email); err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(host)
	}
	return s.Store.Domain(ctx, host) //nolint:wrapcheck // read by identity
}

// RemoveDomain stops routing a hostname.
func (s *Service) RemoveDomain(ctx context.Context, actor Actor, hostname string) error {
	if !actor.Admin {
		return errNeedsAdmin
	}
	host := site.NormalizeHost(hostname)
	if err := s.Store.RemoveDomain(ctx, host); err != nil {
		return err //nolint:wrapcheck // read by identity
	}
	if s.Invalidate != nil {
		s.Invalidate(host)
	}
	return nil
}

// HostReport is what the deployment's ingress needs to serve the verified
// hostnames: each name, and a cert-manager Certificate covering them.
type HostReport struct {
	Hostnames  []string `json:"hostnames"`
	Unverified []string `json:"unverified"`
	// PagesDomain is the wildcard the pages and previews need, "" for none.
	PagesWildcard string `json:"pages_wildcard,omitempty"`
	// CertManager is a Certificate manifest for the hostnames.
	CertManager string `json:"cert_manager"`
}

// Hosts reports the hostnames and the certificate they need.
func (s *Service) Hosts(ctx context.Context) (*HostReport, error) {
	domains, err := s.Store.Domains(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	rep := &HostReport{Hostnames: []string{}, Unverified: []string{}}
	for _, d := range domains {
		if d.Verified() {
			rep.Hostnames = append(rep.Hostnames, d.Hostname)
		} else {
			rep.Unverified = append(rep.Unverified, d.Hostname)
		}
	}
	names := slices.Clone(rep.Hostnames)
	if s.Config.PagesDomain != "" {
		rep.PagesWildcard = "*." + s.Config.PagesDomain
		names = append(names, rep.PagesWildcard)
	}
	rep.CertManager = certManifest(names)
	return rep, nil
}

// certManifest is a cert-manager Certificate for names. The names passed
// ValidateHostname or are the configured pages domain, so each is a plain DNS
// name in a YAML scalar.
func certManifest(names []string) string {
	var b strings.Builder
	_, _ = b.WriteString("apiVersion: cert-manager.io/v1\nkind: Certificate\nmetadata:\n  name: platform-sites\nspec:\n" +
		"  secretName: platform-sites-tls\n  issuerRef:\n    kind: ClusterIssuer\n    name: letsencrypt\n  dnsNames:\n")
	for _, n := range names {
		_, _ = b.WriteString("    - \"" + n + "\"\n")
	}
	return b.String()
}
