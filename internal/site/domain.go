package site

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Domain maps one hostname to a site environment or to an application. It
// routes as soon as it is added: the platform does not check DNS, certificates
// or who owns the name, and whoever runs the deployment adds its ingress.
type Domain struct {
	Hostname    string `json:"hostname" example:"www.example.com"`
	SiteID      string `json:"site_id,omitempty"`
	Site        string `json:"site,omitempty" example:"marketing"`
	Environment string `json:"environment,omitempty" example:"prod"`
	// ApplicationID and Application name the application whose endpoints the
	// hostname serves.
	ApplicationID string `json:"application_id,omitempty"`
	Application   string `json:"application,omitempty" example:"shop"`
	// Public answers anyone at this hostname (#2619): the one way a site
	// environment opens to someone who has not signed in. A domain is added
	// private unless public is asked for.
	Public bool `json:"public"`
	// AccessChangedAt is when Public last changed; an approval to make the
	// domain public is for the access it was asked against.
	AccessChangedAt time.Time `json:"-"`
	// Primary marks the environment's canonical hostname (#2687): at most one
	// domain of an environment is primary, and the domains that redirect send
	// their visitors to it.
	Primary bool `json:"primary"`
	// Mode is DomainServe (the release is served here) or DomainRedirect
	// (every request is answered with a permanent redirect to the primary).
	Mode string `json:"mode" enums:"serve,redirect"`
	// RedirectsTo is the primary hostname a redirect domain sends visitors to,
	// read with the domain, empty for one that serves.
	RedirectsTo string `json:"redirects_to,omitempty" example:"example.com"`
	// primaryPublic is whether that primary answers anyone.
	primaryPublic bool
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	// Routing is the Site routing script's status for the hostname (#2659),
	// absent when no script is chosen.
	Routing *Routing `json:"routing,omitempty"`
}

// The modes of a domain (#2687).
const (
	DomainServe    = "serve"
	DomainRedirect = "redirect"
)

// ValidMode reports whether m is a domain mode.
func ValidMode(m string) bool { return m == DomainServe || m == DomainRedirect }

// SetPrimaryAccess records whether the primary a redirect domain points at is
// public, as the store reads it beside the domain.
func (d *Domain) SetPrimaryAccess(public bool) { d.primaryPublic = public }

// RedirectRoute is what a redirect domain answers: a permanent redirect to its
// environment's primary, answered to anyone when the primary is public and to
// those who may open the environment when it is not. It never reads a release.
func (d *Domain) RedirectRoute() *Route {
	return &Route{
		SiteID: d.SiteID, Site: d.Site, Environment: d.Environment, Hostname: d.Hostname,
		Private: !d.primaryPublic, PrimaryHost: d.RedirectsTo,
	}
}

// Routing is the Site routing integration's status for one hostname (#2659):
// pending until the script answers, then ready or failed with the script's
// message. When the script itself failed, Message is one sentence and
// ScriptFailed is set; RunID, RunScriptID (where the run's page is) and
// RunError, the run's own error, are filled for an administrator only.
type Routing struct {
	Status       string     `json:"status" enums:"pending,ready,failed"`
	Message      string     `json:"message"`
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
	ScriptFailed bool       `json:"script_failed,omitempty"`
	RunID        string     `json:"run_id,omitempty"`
	RunScriptID  string     `json:"run_script_id,omitempty"`
	RunError     string     `json:"run_error,omitempty"`
}

// RoutingRetry is the answer to a Site routing retry (#2659).
type RoutingRetry struct {
	Site    string `json:"site"`
	Retried bool   `json:"retried"`
}

// Route is what a hostname serves.
type Route struct {
	SiteID      string
	Site        string
	Environment string
	// Release is the release served, nil for an environment with none.
	Release *Release
	// Application is the application whose endpoints and pages the host
	// serves, empty for a site.
	Application string
	// Hostname is the added hostname as stored, empty for an address on the
	// pages domain.
	Hostname string
	// Private is true unless the host is a public domain (#2619): every address
	// on the pages domain is private, whatever the environment's domains are.
	// A private host answers only to people who have signed in and may open
	// the environment.
	Private bool
	// PartOf is the application this environment is a part of, "" for none;
	// its endpoints answer on the environment's own hosts.
	PartOf string
	// Status is what the environment answers in place of its release, after
	// the access check (#2631, #2632); its zero value is online.
	Status EnvironmentStatus
	// PrimaryHost is set for a redirect domain (#2687): every request is
	// answered with a permanent redirect to it, after the access check.
	PrimaryHost string
}

// ClaimRules are what a hostname a domain maps must stay clear of: the pages
// domain the platform assigns, the hostnames it answers on itself, and the
// session cookie's domain, under which a served page would receive the
// portal's session.
type ClaimRules struct {
	PagesDomain  string
	PortalHost   string
	Reserved     []string
	CookieDomain string
}

// PublicStamp fixes an approval to make the domain public to the access it
// was asked against: a later change to private and back asks again.
func (d *Domain) PublicStamp() string {
	return "public:" + d.AccessChangedAt.UTC().Format(time.RFC3339Nano)
}

// Claimable refuses a domain whose hostname is not a plain name, or is one
// ClaimRules keep clear. The gateway answers an added hostname before the
// platform's own routes, so a name the platform uses must never be added.
func (d *Domain) Claimable(r ClaimRules) error {
	host := d.Hostname
	if err := ValidateHostname(host); err != nil {
		return err
	}
	cookie := r.CookieDomain
	cookie = strings.TrimPrefix(cookie, ".")
	switch {
	case SameOrUnder(host, r.PagesDomain):
		return fmt.Errorf("hostname %s is under the pages domain, which the platform assigns", host)
	case host == r.PortalHost || slices.ContainsFunc(r.Reserved, func(h string) bool { return SameOrUnder(host, NormalizeHost(h)) }):
		return fmt.Errorf("hostname %s is one this platform answers on itself", host)
	case SameOrUnder(host, cookie):
		return fmt.Errorf("hostname %s is under the session cookie's domain %s, so its pages would receive the portal's session", host, r.CookieDomain)
	}
	return nil
}

// An environment's status (#2631, #2632): what every one of its addresses
// answers. Online serves the release; offline answers 503 with a message;
// redirect answers 302 to another https site. Exactly one applies at a time,
// the release pointer is never touched by any, and dev is always online. The
// rules a status is checked and answered by are internal/site/sitestatus.
const (
	StatusOnline   = "online"
	StatusOffline  = "offline"
	StatusRedirect = "redirect"
)

// The actions History records for a status change.
const (
	ActionOffline  = "offline"
	ActionOnline   = "online"
	ActionRedirect = "redirect"
)

// EnvironmentStatus is what an environment answers in place of its release.
type EnvironmentStatus struct {
	Status string
	// Message is what an offline environment's page says, cleaned.
	Message string
	// BackAt is when visitors are told an offline environment is expected
	// back; it is only what they are told (#2631 rule 7).
	BackAt *time.Time
	// RedirectTo is a redirecting environment's target, as checked and
	// written back when it was set.
	RedirectTo string
	// KeepPath appends the request's cleaned path and query to RedirectTo.
	KeepPath bool
}
