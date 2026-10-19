// Package epwire assembles endpoints (#2170): the binding store, the bot
// check, the runner and manage_script's endpoint commands, from what the
// script wiring already holds. internal/platform/scriptwiring builds it and
// hands the commands to the script layer; the HTTP surface
// (internal/endpoint/ephttp) and the portal routes read the rest through the
// script layer's handle.
package epwire

import (
	"context"
	"database/sql"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/botcheck"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprun"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epstore"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptres"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsrc"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/scripttoken"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Endpoints is the assembled feature.
type Endpoints struct {
	Bindings epbinding.Store
	Checker  *botcheck.Checker
	Runner   *eprun.Runner
	Commands *epcmd.Handler
	// Tokens answers the tokens a script mints and verifies (#2175), in an
	// endpoint request, a run and a draft alike.
	Tokens *scripttoken.Issuer
}

// Deps is what the script wiring hands over.
type Deps struct {
	DB      *sql.DB
	Server  *mcp.Server
	Audit   middleware.AuditLogger
	Metrics *observability.Metrics
	// Subjects resolves where an author's managed resources land.
	Subjects interface {
		ForRun(ctx context.Context, address string) string
	}
	Limits       scriptrun.PlatformLimits
	Destinations []script.Destination
	// Sources reads the managed-resource text a version loads.
	Sources *scriptsrc.Reader
	// Key signs proof-of-work challenges (botcheck.Key); nil leaves the
	// deployment unable to issue one.
	Key []byte
	// Encryptor decrypts the stored secret a bot check provider's key is in.
	Encryptor secretstore.Encryptor
	// BaseURL is the deployment's public address.
	BaseURL string
	// Authority resolves a run's roles to the persona a token key's
	// allow_personas is checked against.
	Authority scripttoken.Authority
}

// Build assembles endpoints, or returns nil with no database: there is
// nowhere to keep a binding.
func Build(deps Deps) *Endpoints {
	if deps.DB == nil {
		return nil
	}
	bindings := epstore.New(deps.DB)
	scripts := scriptstore.New(deps.DB)
	secrets := secretstore.NewStore(deps.DB, deps.Encryptor)
	checker := botcheck.New(deps.DB, deps.Key, func(ctx context.Context, name string) (string, error) {
		return secrets.ConnectionValue(ctx, name, botcheck.SecretUse) //nolint:wrapcheck // the store's refusal names the secret and what allows it
	})
	tokens := &scripttoken.Issuer{
		Keys: func(ctx context.Context, name, persona string, admin bool) (string, error) {
			return secrets.UseValue(ctx, name, scripttoken.SecretUse, persona, admin) //nolint:wrapcheck // the store's refusal names the secret and what allows it
		},
		Uses: scripttoken.NewUses(deps.DB), Authority: deps.Authority,
	}
	runner := &eprun.Runner{
		Bindings: bindings, Scripts: scripts, Server: deps.Server, Audit: deps.Audit, Metrics: deps.Metrics,
		Subjects: deps.Subjects, Limits: deps.Limits, Destinations: deps.Destinations,
		Libraries: libraries(scripts, deps.Sources), Tokens: tokens,
	}
	commands := &epcmd.Handler{
		Store: bindings, PublicAllowed: checker.PublicAllowed, BaseURL: deps.BaseURL,
		ScriptName: func(ctx context.Context, id string) string {
			if sc, err := scripts.GetByID(ctx, id); err == nil && sc != nil {
				return sc.Name
			}
			return ""
		},
	}
	return &Endpoints{Bindings: bindings, Checker: checker, Runner: runner, Commands: commands, Tokens: tokens}
}

// TokenIssuer is the token issuer, nil on nil Endpoints: a deployment with no
// database issues no tokens.
func (e *Endpoints) TokenIssuer() *scripttoken.Issuer {
	if e == nil {
		return nil
	}
	return e.Tokens
}

// libraries is where a version's loads are read from: the library versions,
// and the managed-resource versions the save pinned, as a platform run reads
// them.
func libraries(scripts *scriptstore.Store, sources *scriptsrc.Reader) func(context.Context, *script.Version) (scriptlib.Source, error) {
	return func(ctx context.Context, v *script.Version) (scriptlib.Source, error) {
		pins := map[string]script.ResourcePin{}
		if sources != nil {
			var err error
			if pins, err = scripts.ResourcePins(ctx, v.ID); err != nil {
				return nil, err //nolint:wrapcheck // the store names the read
			}
		}
		return scriptres.With(scripts, sources, pins, nil), nil
	}
}
