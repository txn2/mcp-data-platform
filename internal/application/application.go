// Package application is the record of an application built on the platform
// (#2411): one name for everything a bespoke system is made of — an
// application database schema with its migrations, managed scripts (an
// endpoint or a form is its script's binding), webhook sources and the portal
// assets that are its user interface — and its releases, each of which pins
// the migration version and every script's and asset's version.
//
// While a script or an asset belongs to a released application, the version
// its application's current release pins is the one a run executes and the
// portal serves; saving it changes nothing live until the next release. A
// part belongs to at most one application.
//
// An application has one owner. Everyone signed in reads it; its owner and
// administrators edit its record and its parts; administrators release, roll
// back, export and import, because a release changes the code behind a public
// endpoint and runs migrations on the target database.
package application

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The kinds of part.
const (
	KindScript = "script"
	KindAsset  = "asset"
	KindSource = "source"
	// KindSchema is the application database schema the application holds
	// (#2539), at most one, identified as "<connection>/<schema>". Its
	// migrations are applied by releases; it is not pinned, since a release
	// records the migration version instead.
	KindSchema = "schema"
	// KindSite is one environment of a static site (#2518), identified as
	// "<site>/<environment>". A release pins the site's newest release number
	// and points the environment at it.
	KindSite = "site"
)

// Kinds is every kind of part, in the order a listing shows them.
var Kinds = []string{KindScript, KindAsset, KindSource, KindSchema, KindSite}

// The actions a release row records.
const (
	ActionRelease  = "release"
	ActionRollback = "rollback"
	ActionImport   = "import"
)

// Errors a caller reads by identity.
var (
	ErrNotFound   = errors.New("application not found")
	ErrNameTaken  = errors.New("an application with that name exists")
	ErrSchemaHeld = errors.New("another application holds that schema")
	ErrPartTaken  = errors.New("the part belongs to another application")
	ErrNoPart     = errors.New("the application has no such part")
	ErrNotAllowed = errors.New("not allowed")
	ErrNoRelease  = errors.New("the application has no such release")
	// ErrStale is a release recorded against a current release another
	// release has since moved.
	ErrStale = errors.New("another release of the application was recorded first")
)

// Application is the record.
type Application struct {
	ID          string `json:"id" example:"app_01J9..."`
	Name        string `json:"name" example:"shop"`
	Description string `json:"description" example:"The storefront, its API and its orders"`
	OwnerEmail  string `json:"owner_email" example:"jane@example.com"`
	// Connection and Schema are the application database schema whose
	// migrations a release applies. Empty for an application with none.
	Connection string `json:"connection" example:"orders"`
	Schema     string `json:"schema" example:"shop"`
	// SchemaAdoptedVersion is the migration version the schema had applied
	// when it was added to the application (#2539): migrations no release
	// applied, which a rollback never reverts.
	SchemaAdoptedVersion int `json:"schema_adopted_version" example:"0"`
	// CurrentRelease is the number of the release in force, 0 before the
	// first.
	CurrentRelease int `json:"current_release" example:"3"`
	// ContractVersion is the contract its front ends call (#2679), raised by
	// hand when a release breaks it; 0 declares none, and every front end is
	// compatible.
	ContractVersion int       `json:"contract_version" example:"3"`
	CreatedBy       string    `json:"created_by" example:"jane@example.com"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Parts           []Part    `json:"parts"`
	// Environments are where the application runs, in promotion order: a
	// release lands in the first, a promote copies an environment's release
	// to the next, and the last is what /endpoints/<name> and every handler
	// naming no environment run (#2696).
	Environments []Environment `json:"environments"`
}

// Environment is one place the application runs (#2696).
type Environment struct {
	Name string `json:"name" example:"dev"`
	// Release is the release it runs, 0 for none yet.
	Release int `json:"release" example:"8"`
	// RequiresApproval makes an agent's promote or rollback into it wait on a
	// person, as a site environment's does.
	RequiresApproval bool      `json:"requires_approval"`
	UpdatedBy        string    `json:"updated_by" example:"jane@example.com"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Last is the environment /endpoints/<name> runs, nil for none.
func (a *Application) Last() *Environment {
	if len(a.Environments) == 0 {
		return nil
	}
	return &a.Environments[len(a.Environments)-1]
}

// EnvironmentNamed is the environment called name and its position; "" names
// the last.
func (a *Application) EnvironmentNamed(name string) (*Environment, int, bool) {
	if name == "" {
		if l := a.Last(); l != nil {
			return l, len(a.Environments) - 1, true
		}
		return nil, -1, false
	}
	for i := range a.Environments {
		if a.Environments[i].Name == name {
			return &a.Environments[i], i, true
		}
	}
	return nil, -1, false
}

// Move is one environment moved to a release: Number, decided while it ran
// Expect, by By (#2696).
type Move struct {
	Environment string
	Number      int
	Expect      int
	By          string
}

// Variable is NAME=value in one environment, or the application-wide default
// when Environment is empty (#2696). A value is plain text; a stored secret
// is referenced in it as {{secret:<name>}} and filled when a request is sent.
type Variable struct {
	Environment string    `json:"environment" example:"prod"`
	Name        string    `json:"name" example:"API_BASE"`
	Value       string    `json:"value" example:"https://pay.example.com"`
	UpdatedBy   string    `json:"updated_by" example:"jane@example.com"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// VariablesFor is the variables a run in env sees: the defaults overlaid by
// env's own.
func VariablesFor(vars []Variable, env string) map[string]string {
	out := map[string]string{}
	for _, v := range vars {
		if v.Environment == "" {
			out[v.Name] = v.Value
		}
	}
	for _, v := range vars {
		if v.Environment == env && env != "" {
			out[v.Name] = v.Value
		}
	}
	return out
}

// EnvironmentStore keeps an application's environments and variables
// (#2696).
type EnvironmentStore interface {
	// AddEnvironment inserts e after the environment named after, "" for
	// first.
	AddEnvironment(ctx context.Context, applicationID string, e Environment, after string) error
	// RemoveEnvironment removes an environment and its variables.
	RemoveEnvironment(ctx context.Context, applicationID, name string) error
	// SetEnvironmentRelease makes the environment m names run m.Number,
	// failing with ErrStale when it no longer runs m.Expect. Setting the last
	// environment's release makes it current and holds its pins.
	SetEnvironmentRelease(ctx context.Context, applicationID string, m Move) error
	// SetEnvironmentApproval changes whether an agent's move into it waits.
	SetEnvironmentApproval(ctx context.Context, applicationID, name string, requires bool, by string) error
	Variables(ctx context.Context, applicationID string) ([]Variable, error)
	SetVariable(ctx context.Context, applicationID string, v Variable) error
	DeleteVariable(ctx context.Context, applicationID, environment, name string) error
}

// Part is one member of an application.
type Part struct {
	Kind string `json:"kind" example:"script"`
	// ID is the script's id, the asset's id, or the webhook source's name.
	ID      string    `json:"id" example:"5b0c..."`
	AddedBy string    `json:"added_by" example:"jane@example.com"`
	AddedAt time.Time `json:"added_at"`
	// ReleaseMode is a site environment part's (#2679): linked, served as a
	// front end of the application and moved only by promote and rollback,
	// or coupled, moved one step by every application release and returned
	// by every rollback. "" for every other kind.
	ReleaseMode string `json:"release_mode,omitempty" example:"linked"`
}

// The release modes a site environment part takes (#2679).
const (
	ModeLinked  = "linked"
	ModeCoupled = "coupled"
)

// ValidMode reports whether mode is a site part's release mode.
func ValidMode(mode string) bool { return mode == ModeLinked || mode == ModeCoupled }

// Pin is the version a release holds a script or an asset at.
type Pin struct {
	Kind    string `json:"kind" example:"script"`
	PartID  string `json:"part_id" example:"5b0c..."`
	Version int    `json:"version" example:"4"`
}

// PinChange is a part whose pinned version a release moved. From is 0 for a
// part the previous release did not pin; To is 0 for one this release does
// not.
type PinChange struct {
	Kind   string `json:"kind"`
	PartID string `json:"part_id"`
	From   int    `json:"from"`
	To     int    `json:"to"`
}

// Changes is what a release did.
type Changes struct {
	Parts []PinChange `json:"parts"`
	// Applied and Reverted are the migration versions the release applied or
	// reverted, in the order it ran them.
	Applied  []int `json:"applied"`
	Reverted []int `json:"reverted"`
	// MigrationsKept says why a rollback left migrations applied: a
	// migration between the two releases has no down migration.
	MigrationsKept string `json:"migrations_kept,omitempty"`
	// Findings are what the application's diagram found at the release that
	// did not refuse it (#2540).
	Findings []string `json:"findings,omitempty"`
	// Forced is each site part an administrator released over though its
	// release does not work with the contract (#2679).
	Forced []string `json:"forced,omitempty"`
}

// Release is one release row and its pins.
type Release struct {
	Number int    `json:"number" example:"3"`
	Action string `json:"action" example:"release"`
	// MigrationVersion is the highest migration the release holds applied.
	MigrationVersion int `json:"migration_version" example:"7"`
	// Restored is the release a rollback restored the pins of.
	Restored  int       `json:"restored,omitempty" example:"2"`
	Changes   Changes   `json:"changes"`
	Note      string    `json:"note,omitempty"`
	CreatedBy string    `json:"created_by" example:"admin@example.com"`
	CreatedAt time.Time `json:"created_at"`
	Pins      []Pin     `json:"pins"`
}

// PinOf is the version the release pins the part at, 0 for none.
func (r *Release) PinOf(kind, id string) int {
	if r == nil {
		return 0
	}
	for _, p := range r.Pins {
		if p.Kind == kind && p.PartID == id {
			return p.Version
		}
	}
	return 0
}

// Diff is the pins that move from prev to next, in next's order and then
// prev's pins that next drops.
func Diff(prev, next []Pin) []PinChange {
	before := make(map[[2]string]int, len(prev))
	for _, p := range prev {
		before[[2]string{p.Kind, p.PartID}] = p.Version
	}
	out := []PinChange{}
	seen := make(map[[2]string]bool, len(next))
	for _, p := range next {
		k := [2]string{p.Kind, p.PartID}
		seen[k] = true
		if before[k] != p.Version {
			out = append(out, PinChange{Kind: p.Kind, PartID: p.PartID, From: before[k], To: p.Version})
		}
	}
	for _, p := range prev {
		if !seen[[2]string{p.Kind, p.PartID}] {
			out = append(out, PinChange{Kind: p.Kind, PartID: p.PartID, From: p.Version})
		}
	}
	return out
}

// nameRE is what an application's name may be: a lowercase slug.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// MaxDescription bounds the description.
const MaxDescription = 2000

// Validate checks the record's own fields.
func (a *Application) Validate() error {
	if !nameRE.MatchString(a.Name) {
		return fmt.Errorf("name %q: a lowercase letter, then lowercase letters, digits and hyphens, at most 63", a.Name)
	}
	if len(a.Description) > MaxDescription {
		return fmt.Errorf("description: at most %d bytes", MaxDescription)
	}
	if (a.Connection == "") != (a.Schema == "") {
		return errors.New("connection and schema are named together, or neither")
	}
	if a.OwnerEmail == "" {
		return errors.New("an application needs an owner with an email")
	}
	for _, p := range a.Parts {
		if !ValidKind(p.Kind) {
			return fmt.Errorf("part %s: kind %q is not one of %v", p.ID, p.Kind, Kinds)
		}
	}
	return nil
}

// ValidKind reports whether kind names a kind of part.
func ValidKind(kind string) bool {
	return kind == KindScript || kind == KindAsset || kind == KindSource || kind == KindSchema || kind == KindSite
}

// SchemaPartID is the id the schema part goes by: "<connection>/<schema>".
func SchemaPartID(connection, schema string) string { return connection + "/" + schema }

// ParseSchemaPartID reads a schema part's id: "<connection>/<schema>", or
// the schema alone, which leaves connection empty for the caller to choose.
func ParseSchemaPartID(id string) (connection, schema string) {
	id = strings.TrimSpace(id)
	if c, s, ok := strings.Cut(id, "/"); ok {
		return strings.TrimSpace(c), strings.TrimSpace(s)
	}
	return "", id
}

// ParseSitePartID reads a site part's id: "<site>/<environment>".
func ParseSitePartID(id string) (siteName, environment string, ok bool) {
	siteName, environment, ok = strings.Cut(strings.TrimSpace(id), sitePartSep)
	return siteName, environment, ok && siteName != "" && environment != ""
}

// sitePartSep separates a site part id's site from its environment.
const sitePartSep = "/"

// SitePartID is a site part's id, the form ParseSitePartID reads.
func SitePartID(siteName, environment string) string { return siteName + sitePartSep + environment }

// SiteEnvironment is one environment of a site, as the search for parts to
// add lists it (#2613).
type SiteEnvironment struct {
	Site        string
	Environment string
	Owner       string
	UpdatedAt   time.Time
}

// HeldSchema is a schema an application takes on: where it is, the
// migration version it had applied, who added it, and the run role its
// scripts' statements log in as, with the role's password sealed.
type HeldSchema struct {
	Connection     string
	Schema         string
	AdoptedVersion int
	AddedBy        string
	Role           string
	SealedPassword string
}

// RunRole is an application's run role as stored: the role, and its
// password sealed.
type RunRole struct {
	ApplicationID  string
	Connection     string
	Schema         string
	Role           string
	SealedPassword string
}

// ErrNoRunRole is an application holding no run role.
var ErrNoRunRole = errors.New("the application has no run role")

// Actor is who acts on an application. Subject and Roles are the session's
// identity and authority, which a part an import writes runs under: they
// come from the caller's own session, MCP or portal alike.
type Actor struct {
	Email   string
	Admin   bool
	Subject string
	Roles   []string
}

// MayEdit reports whether the actor edits the application's record and
// parts: its owner or an administrator.
func (a *Application) MayEdit(actor Actor) bool {
	return actor.Admin || (actor.Email != "" && actor.Email == a.OwnerEmail)
}

// Recorded is a release to record: the row, its pins, and the current
// release it was decided against.
type Recorded struct {
	Release Release
	// After is the release the environment ran when the release was decided;
	// the record fails with ErrStale when another release moved it.
	After int
	// Environment is where the release lands, "" for the last (#2696).
	Environment string
}

// Store keeps applications, their parts and their releases.
type Store interface {
	Create(ctx context.Context, a *Application) error
	Get(ctx context.Context, id string) (*Application, error)
	GetByName(ctx context.Context, name string) (*Application, error)
	List(ctx context.Context) ([]Application, error)
	// Update writes the description and owner.
	Update(ctx context.Context, a *Application) error
	// Delete removes the application, its parts and releases, and clears the
	// released version its parts were held at.
	Delete(ctx context.Context, id string) error
	AddPart(ctx context.Context, applicationID string, p Part) error
	// SetPartMode changes a site environment part's release mode (#2679).
	SetPartMode(ctx context.Context, applicationID, id, mode string) error
	// RemovePart removes the part and clears the released version it was
	// held at.
	RemovePart(ctx context.Context, applicationID, kind, id string) error
	// Owning is the application a part belongs to, ErrNotFound for none.
	Owning(ctx context.Context, kind, id string) (*Application, error)
	// SetSchema makes the application hold a schema and stores its run role,
	// in one transaction: ErrSchemaHeld when another application holds it.
	SetSchema(ctx context.Context, applicationID string, h HeldSchema) error
	// SetRunRole stores the run role of the schema the application holds.
	SetRunRole(ctx context.Context, applicationID string, h HeldSchema) error
	// ClearSchema takes the schema and its run role off the application.
	ClearSchema(ctx context.Context, applicationID string) error
	// Holding is the application holding schema on connection, ErrNotFound
	// for none.
	Holding(ctx context.Context, connection, schema string) (*Application, error)
	// RunRoleOf is the application's run role, ErrNoRunRole for none.
	RunRoleOf(ctx context.Context, applicationID string) (*RunRole, error)
	Releases(ctx context.Context, applicationID string) ([]Release, error)
	Release(ctx context.Context, applicationID string, number int) (*Release, error)
	// Record writes a release and its pins, makes it current, and holds each
	// pinned part at its version, in one transaction.
	Record(ctx context.Context, applicationID string, r Recorded) (*Release, error)
}

var (
	envNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	varNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
)

// MaxVariableValue is the most bytes a variable's value holds.
const MaxVariableValue = 8 << 10

// CheckEnvironment refuses an environment name the store would not hold.
func CheckEnvironment(e Environment) error {
	if !envNameRE.MatchString(e.Name) {
		return fmt.Errorf("environment %q: a lowercase letter, then lowercase letters, digits and hyphens, at most 32", e.Name)
	}
	return nil
}

// CheckVariable refuses a variable the store would not hold.
func CheckVariable(v Variable) error {
	if !varNameRE.MatchString(v.Name) {
		return fmt.Errorf("variable %q: an uppercase letter or underscore, then uppercase letters, digits and underscores, at most 64", v.Name)
	}
	if len(v.Value) > MaxVariableValue {
		return fmt.Errorf("variable %s: at most %d bytes", v.Name, MaxVariableValue)
	}
	return nil
}
