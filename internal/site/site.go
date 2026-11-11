// Package site is the record of a static site served by the platform (#2518):
// a site with named environments, each pointing at one immutable release (a
// file set whose objects are kept by digest, per site), and the hostnames that
// route to an environment or to an application's endpoints.
//
// Publishing adds a release, from a complete file set or derived from another
// release by changing part of it. Promoting and rolling back only move an
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

// EnvDev is the working copy (#2604): every publish, patch and in-place edit
// lands here and nowhere else, and people use the environments a release is
// promoted to. A new site starts with it, holding the main address.
const EnvDev = "dev"

// EnvAlias is the name EnvDev had before #2604, accepted for one minor release.
const EnvAlias = "default"

// CanonicalEnvironment is name with the old alias read as EnvDev; aliased
// reports that it was, so the caller can say so.
func CanonicalEnvironment(name string) (canonical string, aliased bool) {
	if name == EnvAlias {
		return EnvDev, true
	}
	return name, false
}

// AliasNote is what a response says when it read the old name.
const AliasNote = `"default" is now "dev": the working copy every publish and edit lands in. "default" is accepted until the next minor release.`

// The actions a deployment records.
const (
	ActionPublish     = "publish"
	ActionPromote     = "promote"
	ActionRollback    = "rollback"
	ActionApplication = "application"
)

// reservedHyphens is refused in a name: a DNS label with hyphens there is read
// as an encoded international name.
const reservedHyphens = "--"

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
	ErrNoGrant       = errors.New("that person has no access to this site")
	ErrNoJob         = errors.New("no such job")
)

// Site is the record.
type Site struct {
	ID          string `json:"id" example:"site_01J9"`
	Name        string `json:"name" example:"marketing"`
	Description string `json:"description"`
	OwnerEmail  string `json:"owner_email" example:"jane@example.com"`
	// MainEnvironment is the environment the main address,
	// <site>.<pages domain>, serves; "" is none, and the address answers not
	// found.
	MainEnvironment string        `json:"main_environment" example:"prod"`
	Environments    []Environment `json:"environments"`
	CreatedBy       string        `json:"created_by"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// Environment is one named place a release is served from.
type Environment struct {
	Name string `json:"name" example:"prod"`
	// Release is the number of the release served, 0 before the first.
	Release int `json:"release" example:"3"`
	// RequiresApproval makes a promotion here, and making it public, a request
	// in the Approvals queue.
	RequiresApproval bool `json:"requires_approval"`
	// Public answers to anyone. A private environment answers only to
	// administrators, the owner, the people who manage the site and whoever it
	// is shared with, after they sign in. EnvDev is never public.
	Public bool `json:"public"`
	// InternalView opens a private environment to everyone signed in whose
	// persona is not external: a company intranet.
	InternalView bool `json:"internal_view"`
	// AccessChangedAt is when Public last changed; an approval to make the
	// environment public is for the access it was asked against.
	AccessChangedAt time.Time `json:"-"`
	UpdatedBy       string    `json:"updated_by,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// EnvironmentSettings is a change to an environment; a nil field is left
// alone, and a new environment takes false for each.
type EnvironmentSettings struct {
	RequiresApproval *bool
	Public           *bool
	InternalView     *bool
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
	// SetEnvironment creates an environment, or changes its settings.
	SetEnvironment(ctx context.Context, siteID, name string, set EnvironmentSettings, actor string) error
	// SetMainEnvironment gives the main address to an environment, "" to none.
	SetMainEnvironment(ctx context.Context, siteID, env string) error
	// RecordRelease writes a release and its files, numbering it next. With a
	// move it also points the environment at the new release, in the one
	// transaction, so a move that loses to another leaves no release behind.
	RecordRelease(ctx context.Context, r *Release, move *Pointer) error
	Release(ctx context.Context, siteID string, number int) (*Release, error)
	ReleaseByID(ctx context.Context, id string) (*Release, error)
	Releases(ctx context.Context, siteID string) ([]Release, error)
	// Files is what a release serves, in path order, however it is stored.
	Files(ctx context.Context, releaseID string) ([]File, error)
	// Tree is one tree of a release, in path order.
	Tree(ctx context.Context, releaseID, tree string) ([]File, error)
	// StoredFiles lists what a site holds in object storage, each object once,
	// whichever releases share it.
	StoredFiles(ctx context.Context, siteID string) ([]File, error)
	// Point moves an environment, failing with ErrStale when another move
	// got there first, and records the deployment.
	Point(ctx context.Context, p Pointer) error
	Deployments(ctx context.Context, siteID, environment string, limit int) ([]Deployment, error)
	AddDomain(ctx context.Context, d *Domain) error
	Domain(ctx context.Context, hostname string) (*Domain, error)
	Domains(ctx context.Context) ([]Domain, error)
	SiteDomains(ctx context.Context, siteID string) ([]Domain, error)
	RemoveDomain(ctx context.Context, hostname string) error
	// RouteHost is what an added hostname serves, ErrNoDomain for none.
	RouteHost(ctx context.Context, hostname string) (*Route, error)
	// RouteOwn is what a site's main address serves, ErrNoDomain for none.
	RouteOwn(ctx context.Context, siteName string) (*Route, error)
	// RouteEnv is what <site>--<env> serves, ErrNoDomain for none.
	RouteEnv(ctx context.Context, siteName, env string) (*Route, error)
	// SiteAccess reads what decides whether a person may open one
	// environment, fresh.
	SiteAccess(ctx context.Context, siteID, env, email, persona string) (*Access, error)
	Grants(ctx context.Context, siteID string) ([]Grant, error)
	GrantLevel(ctx context.Context, siteID, email string) (string, error)
	// SetGrant adds a person or persona, or changes their grant.
	SetGrant(ctx context.Context, siteID string, g *Grant) error
	// RemoveGrant removes the grant to an email or, with email "", a persona.
	RemoveGrant(ctx context.Context, siteID, email, persona string) error
	CreateJob(ctx context.Context, j *Job) error
	Job(ctx context.Context, id string) (*Job, error)
	ActiveJobs(ctx context.Context, siteID string) ([]Job, error)
	// ReportJob records a running job's progress and says whether it was asked
	// to stop.
	ReportJob(ctx context.Context, id string, p JobProgress) (stop bool, err error)
	FinishJob(ctx context.Context, id string, r JobResult) error
	StopJob(ctx context.Context, id string) error
}

var (
	nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	envRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// ValidateName checks a site's name.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) || strings.Contains(name, reservedHyphens) || strings.HasSuffix(name, "-") {
		return fmt.Errorf("site name %q: a lowercase letter, then lowercase letters, digits and single hyphens, at most 63", name)
	}
	return nil
}

// ValidateEnvironment checks an environment's name.
func ValidateEnvironment(name string) error {
	if !envRE.MatchString(name) || strings.Contains(name, reservedHyphens) || strings.HasSuffix(name, "-") {
		return fmt.Errorf("environment name %q: a lowercase letter, then lowercase letters, digits and single hyphens, at most 32", name)
	}
	return nil
}

// maxLabel is the longest a DNS label may be.
const maxLabel = 63

// idnPrefix starts a label a resolver reads as an encoded international name.
const idnPrefix = "xn--"

// EnvLabel is the one DNS label <site>--<env> under the pages domain: every
// environment's own address.
func EnvLabel(siteName, env string) string { return siteName + reservedHyphens + env }

// ParseEnvLabel splits a <site>--<env> label. Neither name may hold "--", so
// the first is the separator.
func ParseEnvLabel(label string) (siteName, env string, ok bool) {
	siteName, env, ok = strings.Cut(label, reservedHyphens)
	return siteName, env, ok && siteName != "" && env != ""
}

// ValidateLabel refuses an environment of a site whose address could not be
// one DNS label: longer than 63, or read as an international name.
func ValidateLabel(siteName, env string) error {
	label := EnvLabel(siteName, env)
	if len(label) > maxLabel {
		return fmt.Errorf("address %s is %d characters, and one part of an address can be at most %d: choose a name of %d characters or fewer",
			label, len(label), maxLabel, max(1, maxLabel-len(siteName)-len(reservedHyphens)))
	}
	if strings.HasPrefix(label, idnPrefix) {
		return fmt.Errorf("address %s would be read as an encoded international domain name: a site cannot be named xn", label)
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
