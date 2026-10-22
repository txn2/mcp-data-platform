package extaccountwire

import (
	"context"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/extaccount"
	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Platform is what the assembly takes from the HTTP layer rather than the
// platform: the browser-session signing key, and the admin API's
// authentication and author, nil when the admin API is off.
type Platform struct {
	SigningKey []byte
	AdminWrap  func(http.Handler) http.Handler
	Author     func(*http.Request) string
}

// MountPlatform assembles the feature from the running platform and mounts
// it; see Mount.
func MountPlatform(mux *http.ServeMux, p *platform.Platform, notify *notifydelivery.Handle, h Platform) *extaccount.Service {
	bs := p.Config().Auth.BrowserSession
	d := Deps{
		DB:            p.DB(),
		Personas:      p.PersonaRegistry(),
		StoredPersona: storedPersona(p),
		Users:         p.UserStore(),
		Cookie: browsersession.CookieConfig{
			Name: bs.CookieName, Domain: bs.Domain, Secure: bs.IsSecure(),
			SameSite: browsersession.ParseSameSite(bs.SameSite), Key: h.SigningKey,
		},
		BaseURL:     p.Config().Portal.PublicBaseURL,
		SessionAuth: p.BrowserSessionAuth(),
		Flow:        p.BrowserSessionFlow(),
		AdminWrap:   h.AdminWrap,
		Author:      h.Author,
	}
	if notify != nil {
		d.Mail = func(ctx context.Context, to, name, link string, invitation bool, valid time.Duration) error {
			return notify.SendExternalSignIn(ctx, notifydelivery.SignIn{To: to, Name: name, Link: link, Invitation: invitation, Valid: valid})
		}
	}
	return Mount(mux, d)
}

// storedPersona reads a persona from the platform's persona store, which a
// replica that has not yet heard of a new persona still answers from.
func storedPersona(p *platform.Platform) func(ctx context.Context, name string) (*persona.Persona, bool) {
	return func(ctx context.Context, name string) (*persona.Persona, bool) {
		if p.PersonaStore() == nil {
			return nil, false
		}
		def, err := p.PersonaStore().Get(ctx, name)
		if err != nil || def == nil {
			return nil, false
		}
		return def.ToPersona(), true
	}
}
