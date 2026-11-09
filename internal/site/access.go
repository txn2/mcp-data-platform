package site

import (
	"strings"
	"time"
)

// The levels of access someone can be given to a site.
const (
	// LevelManage publishes, promotes, rolls back, adds domains and shares
	// view access. It cannot add people who manage, change the owner or delete
	// the site.
	LevelManage = "manage"
	// LevelView opens the site's own address and gives nothing else.
	LevelView = "view"
)

// Grant is one person's access to a site, keyed to the email the identity
// provider reports.
type Grant struct {
	Email     string    `json:"email" example:"pat@client.example"`
	Level     string    `json:"level" example:"view"`
	GrantedBy string    `json:"granted_by" example:"jane@example.com"`
	CreatedAt time.Time `json:"created_at"`
}

// ValidLevel reports whether level is one a grant can hold.
func ValidLevel(level string) bool { return level == LevelManage || level == LevelView }

// NormalizeEmail is the form a grant is stored and matched in.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// Access is what the store knows about one person and one site: read fresh on
// every request to a site's own address, so that removing someone takes effect
// on their next request.
type Access struct {
	OwnerEmail   string
	InternalView bool
	// Level is the person's grant, "" for none.
	Level string
}
