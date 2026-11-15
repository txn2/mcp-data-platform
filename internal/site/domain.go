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
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
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
