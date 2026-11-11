package site

import (
	"slices"
	"strings"
	"time"
)

// The levels of access someone can be given to a site.
const (
	// LevelManage publishes, promotes, rolls back, adds domains and shares
	// view access. It cannot add people who manage, change the owner or delete
	// the site.
	LevelManage = "manage"
	// LevelView opens the site's own address and reads the files it serves now
	// through manage_asset. It changes nothing and sees no earlier release.
	LevelView = "view"
)

// Grant is access to a site for one person, keyed to the email the identity
// provider reports, or for everyone holding a persona. A persona grant is
// always view, matched against the viewer's persona on every request.
type Grant struct {
	Email   string `json:"email" example:"pat@client.example"`
	Persona string `json:"persona" example:"finance-team"`
	Level   string `json:"level" example:"view"`
	// Environments are the private environments a view grant opens; none is
	// every private environment. A manage grant is site-wide.
	Environments []string  `json:"environments"`
	GrantedBy    string    `json:"granted_by" example:"jane@example.com"`
	CreatedAt    time.Time `json:"created_at"`
}

// Opens reports whether a view grant opens env.
func (g *Grant) Opens(env string) bool {
	return len(g.Environments) == 0 || slices.Contains(g.Environments, env)
}

// ValidLevel reports whether level is one a grant can hold.
func ValidLevel(level string) bool { return level == LevelManage || level == LevelView }

// NormalizeEmail is the form a grant is stored and matched in.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Access is what the store knows about one person and one environment of a
// site: read fresh on every request to a private environment, so that
// removing someone, or changing a persona, takes effect on their next request.
type Access struct {
	OwnerEmail string
	// InternalView is the environment's own setting.
	InternalView bool
	// ByEmail is true when a grant to the person's email opens the
	// environment: they manage the site, or a view grant names it.
	ByEmail bool
	// ByPersona is true when a view grant to the person's persona opens it.
	ByPersona bool
}
