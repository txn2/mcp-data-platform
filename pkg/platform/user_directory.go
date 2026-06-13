package platform

import (
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Auth-type labels that represent a real person (as opposed to an API key or
// anonymous session). Only these populate the known-users directory.
const (
	authTypeLabelOIDC  = "oidc"
	authTypeLabelOAuth = "oauth"
)

// initUserStore initializes the known-users directory (#614). The directory
// requires a database; without one the feature is disabled (nil store) and
// every consumer degrades cleanly to free-typed email sharing.
func (p *Platform) initUserStore() {
	if p.db == nil {
		return
	}
	p.userStore = user.NewPostgresStore(p.db)
	p.userDirectory = user.NewDirectory(p.userStore)
	slog.Info("user directory enabled")
}

// UserStore returns the known-users directory store (nil when no database is
// configured).
func (p *Platform) UserStore() user.Store {
	return p.userStore
}

// observeAuthenticatedUser records an authenticated person in the directory.
// It is wired as the UserObserver on the authenticator, so it runs on every
// successful authentication. Only real people (OIDC/OAuth) are recorded; API
// keys and anonymous sessions are not persons to share with. The directory
// itself throttles and writes asynchronously, so this is cheap.
func (p *Platform) observeAuthenticatedUser(info *middleware.UserInfo) {
	if p.userDirectory == nil || info == nil {
		return
	}
	if info.AuthType != authTypeLabelOIDC && info.AuthType != authTypeLabelOAuth {
		return
	}
	first, last := deriveUserName(info)
	p.userDirectory.Observe(info.Email, first, last)
}

// observeBrowserLogin records a portal/admin SPA user in the directory at
// login. The browser-session flow already supplies a split first/last name
// from the id_token, so this routes straight to the directory (which sanitizes,
// throttles, and writes asynchronously). No-op when no database is configured.
func (p *Platform) observeBrowserLogin(email, firstName, lastName string) {
	if p.userDirectory == nil {
		return
	}
	p.userDirectory.Observe(email, firstName, lastName)
}

// deriveUserName extracts a first and last name from a UserInfo, delegating to
// the shared pkg/user derivation so the token and browser-session paths agree.
func deriveUserName(info *middleware.UserInfo) (first, last string) {
	return user.NameFromClaims(info.Claims, info.Name)
}
