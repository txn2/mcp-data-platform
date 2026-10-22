// Package extaccountwire assembles external accounts (#2092) from the
// platform: the service over the platform's database, persona registry,
// mailer and browser-session cookie; its admin and public routes; the judge
// every cookie session passes; and the identity-provider sign-in that
// converts an account.
package extaccountwire

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/admin/extaccountapi"
	"github.com/txn2/mcp-data-platform/internal/extaccount"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Deps is what the feature is assembled from. A deployment without a
// database or a browser-session signing key has no external accounts: the
// session a link opens is a browser session.
type Deps struct {
	DB       *sql.DB
	Personas extaccount.Personas
	// StoredPersona reads a persona from the persona store.
	StoredPersona func(ctx context.Context, name string) (*persona.Persona, bool)
	Users         user.Store
	Mail          extaccount.Mailer
	Cookie        browsersession.CookieConfig
	BaseURL       string
	// SessionAuth is the cookie authenticator every surface reads a session
	// through; the account judge is installed on it.
	SessionAuth *browsersession.Authenticator
	// Flow is the identity-provider sign-in, whose completions convert an
	// account; nil when the deployment has none.
	Flow *browsersession.Flow
	// AdminWrap is the admin API's authentication; nil mounts no admin route.
	AdminWrap func(http.Handler) http.Handler
	Author    func(*http.Request) string
}

// Mount assembles the feature and registers its routes, returning the
// service, or nil when the deployment cannot have external accounts.
func Mount(mux *http.ServeMux, d Deps) *extaccount.Service {
	if d.DB == nil || len(d.Cookie.Key) == 0 || d.SessionAuth == nil {
		return nil
	}
	db := d.DB
	store := extaccount.NewPostgresStore(db)
	settings := settingsStore{section: settingsjson.New[extaccount.Settings](db, extaccount.SettingsSection)}
	svc := extaccount.New(extaccount.Config{
		Store:         store,
		Personas:      d.Personas,
		StoredPersona: d.StoredPersona,
		Settings:      settings.Read,
		KnownInternal: knownInternal(d.Users),
		Mail:          d.Mail,
		RevokeShares:  store.RevokeSharesTo,
		BaseURL:       d.BaseURL,
		Cookie:        d.Cookie,
	})
	d.SessionAuth.SetAdmit(svc.Admit)
	if d.Flow != nil {
		d.Flow.AfterSignIn(svc.ConvertOnIdentityProviderSignIn)
	}
	extaccountapi.RegisterPublic(mux, svc)
	if d.AdminWrap != nil {
		extaccountapi.RegisterAdmin(mux, d.AdminWrap, extaccountapi.Config{
			Service: svc, Settings: settings, SharedItems: store.CountSharesTo, Author: d.Author,
		})
	}
	slog.Info("External accounts enabled: admin API on /api/v1/admin/external-users, sign-in links on " + extaccountapi.RequestPath)
	return svc
}

// knownInternal reports whether an address is already in the people
// directory from an identity-provider or token sign-in.
func knownInternal(users user.Store) func(ctx context.Context, email string) (bool, error) {
	if users == nil {
		return nil
	}
	return func(ctx context.Context, email string) (bool, error) {
		u, err := users.Get(ctx, email)
		if errors.Is(err, user.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err //nolint:wrapcheck // the caller names the check
		}
		return u.Source == user.SourceAuth, nil
	}
}

// settingsStore reads the settings section, falling back to the defaults.
type settingsStore struct {
	section *settingsjson.Section[extaccount.Settings]
}

// Read returns the stored settings, or the defaults when none are stored or
// they cannot be read.
func (s settingsStore) Read(ctx context.Context) extaccount.Settings {
	stored, err := s.section.Get(ctx)
	if err != nil || stored.Value.Validate() != nil {
		return extaccount.Defaults()
	}
	return stored.Value
}

// Write stores the settings.
func (s settingsStore) Write(ctx context.Context, v extaccount.Settings, author string) error {
	return s.section.Set(ctx, v, author) //nolint:wrapcheck // the section names the write
}
