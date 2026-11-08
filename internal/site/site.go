// Package site is the record of a static site served by the platform (#2518):
// a site with named environments, each pointing at one immutable release (a
// built file set under its own object-storage prefix), and the hostnames that
// route to an environment or to an application's endpoints once verified.
//
// Publishing adds a release. Promoting and rolling back only move an
// environment's pointer, so no file is copied and the move takes effect on
// the next request. The serving layer is internal/site/sitegw, and it is the
// same layer that serves an application's browser pages (#2517).
package site

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

// The environment a publish lands in when none is named, and the one created
// requiring approval.
const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

// The actions a deployment records.
const (
	ActionPublish     = "publish"
	ActionPromote     = "promote"
	ActionRollback    = "rollback"
	ActionApplication = "application"
)

// PreviewSeparator divides a preview hostname's first label into environment
// and site. Neither name may contain it, so the label reads one way.
const PreviewSeparator = "--"

// Limits on a record's fields.
const (
	MaxDescription = 2000
	MaxNote        = 2000
	maxHostLen     = 253
	maxRedirects   = 5000
	maxRedirectLen = 2000
)

// Errors a caller reads by identity.
var (
	ErrNotFound      = errors.New("no such site")
	ErrNameTaken     = errors.New("a site with that name exists")
	ErrNoEnvironment = errors.New("the site has no such environment")
	ErrNoRelease     = errors.New("the site has no such release")
	ErrNotAllowed    = errors.New("not allowed")
	ErrStale         = errors.New("another change to the environment was recorded first")
	ErrNoDomain      = errors.New("no such domain")
	ErrDomainTaken   = errors.New("that hostname is already mapped")
)

// Site is the record.
type Site struct {
	ID           string        `json:"id" example:"site_01J9"`
	Name         string        `json:"name" example:"marketing"`
	Description  string        `json:"description"`
	OwnerEmail   string        `json:"owner_email" example:"jane@example.com"`
	Environments []Environment `json:"environments"`
	CreatedBy    string        `json:"created_by"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Environment is one named place a release is served from.
type Environment struct {
	Name string `json:"name" example:"prod"`
	// Release is the number of the release served, 0 before the first.
	Release int `json:"release" example:"3"`
	// RequiresApproval makes a promotion here a request in the Approvals queue.
	RequiresApproval bool      `json:"requires_approval"`
	UpdatedBy        string    `json:"updated_by,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Environment is the named environment, nil for none.
func (s *Site) Environment(name string) *Environment {
	for i := range s.Environments {
		if s.Environments[i].Name == name {
			return &s.Environments[i]
		}
	}
	return nil
}

// Pointer is one move of an environment's pointer.
type Pointer struct {
	SiteID      string
	Environment string
	// After is the release the move was decided against.
	After  int
	To     int
	Action string
	Actor  string
}

// Store keeps sites, releases and domains.
type Store interface {
	Create(ctx context.Context, s *Site) error
	Get(ctx context.Context, id string) (*Site, error)
	GetByName(ctx context.Context, name string) (*Site, error)
	List(ctx context.Context) ([]Site, error)
	Update(ctx context.Context, s *Site) error
	Delete(ctx context.Context, id string) error
	// SetEnvironment creates an environment, or changes whether it requires
	// approval.
	SetEnvironment(ctx context.Context, siteID, name string, requiresApproval bool, actor string) error
	// RecordRelease writes a release and its files, numbering it next.
	RecordRelease(ctx context.Context, r *Release) error
	Release(ctx context.Context, siteID string, number int) (*Release, error)
	ReleaseByID(ctx context.Context, id string) (*Release, error)
	Releases(ctx context.Context, siteID string) ([]Release, error)
	Files(ctx context.Context, releaseID string) ([]File, error)
	// Point moves an environment, failing with ErrStale when another move
	// got there first, and records the deployment.
	Point(ctx context.Context, p Pointer) error
	Deployments(ctx context.Context, siteID, environment string, limit int) ([]Deployment, error)
	AddDomain(ctx context.Context, d *Domain) error
	Domain(ctx context.Context, hostname string) (*Domain, error)
	Domains(ctx context.Context) ([]Domain, error)
	MarkVerified(ctx context.Context, hostname, by string) error
	RemoveDomain(ctx context.Context, hostname string) error
	// RouteHost is what a verified hostname serves, ErrNoDomain for none.
	RouteHost(ctx context.Context, hostname string) (*Route, error)
	// RoutePreview is what an environment's preview hostname serves.
	RoutePreview(ctx context.Context, siteName, environment string) (*Route, error)
}

var (
	nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	envRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// ValidateName checks a site's name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) || strings.Contains(name, PreviewSeparator) || strings.HasSuffix(name, "-") {
		return fmt.Errorf("site name %q: a lowercase letter, then lowercase letters, digits and single hyphens, at most 63", name)
	}
	return nil
}

// ValidateEnvironment checks an environment's name.
func ValidateEnvironment(name string) error {
	if !envRE.MatchString(name) || strings.Contains(name, PreviewSeparator) || strings.HasSuffix(name, "-") {
		return fmt.Errorf("environment name %q: a lowercase letter, then lowercase letters, digits and single hyphens, at most 32", name)
	}
	return nil
}

// Validate checks the record's own fields: its name, its description and each
// environment's name.
func (s *Site) Validate() error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	if len(s.Description) > MaxDescription {
		return fmt.Errorf("description: at most %d bytes", MaxDescription)
	}
	for _, env := range s.Environments {
		if err := ValidateEnvironment(env.Name); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeHost is hostname as a request's Host names it, lowercased, with no
// port or trailing dot.
func NormalizeHost(hostname string) string {
	h := strings.ToLower(strings.TrimSpace(hostname))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

// ValidateHostname checks a hostname a domain may map: a plain DNS name of at
// least two labels, with no wildcard or address.
func ValidateHostname(hostname string) error {
	if len(hostname) > maxHostLen || !hostRE.MatchString(hostname) || net.ParseIP(hostname) != nil {
		return fmt.Errorf("hostname %q: a DNS name such as www.example.com, with no wildcard, port or address", hostname)
	}
	return nil
}

// SameOrUnder reports whether host is domain or a name beneath it.
func SameOrUnder(host, domain string) bool {
	return domain != "" && (host == domain || strings.HasSuffix(host, "."+domain))
}
