// Package scriptlayer is the MCP surface of the managed-script feature: the
// manage_script tool and everything it needs to resolve, authorize, edit,
// validate, and dry-run a script.
//
// It owns the assembly (the Postgres-backed script store) and the tool, and it
// depends on pkg/script for the domain rules and internal/platform/scriptrun
// for the engine. Nothing here decides what Starlark means and nothing here
// re-implements the edit gate; both live one layer down, so the tool is a
// translation from MCP arguments into domain calls.
//
// The MCP server is captured at RegisterTool rather than at construction: the
// store must exist early, while the server exists only once the platform has
// assembled it, and run_draft needs that server to open its in-memory session.
package scriptlayer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Config carries the resolved values the owner needs to assemble the script
// layer. The caller translates its own config into this shape so this package
// stays free of the platform's config types.
type Config struct {
	// DB backs the script store; nil leaves the store nil and manage_script
	// unregistered (there is nowhere to keep a script).
	DB *sql.DB
	// Store, when non-nil, is used directly instead of building a Postgres
	// store from DB. Production passes DB and leaves this nil.
	Store script.Store
	// Runs is the run queue run_script enqueues onto and the run history the
	// run commands read. nil leaves run_script unregistered, which is the
	// correct shape for a deployment that cannot execute scripts at all.
	Runs script.RunStore
	// AdminPersona is the persona name that grants authority over scripts at
	// every scope; matched against the caller's persona in each command.
	AdminPersona string
}

// Handle owns the assembled script layer. All accessors are nil-safe, so a
// deployment without a database holds a Handle that registers nothing.
type Handle struct {
	store script.Store
	// versions is the same store narrowed to its version contract, held
	// separately because run_script must load the code the execution gate
	// points at and a store without versioning cannot answer that.
	versions script.VersionStore
	// schedules is the same store narrowed to its schedule contract, nil where
	// the deployment has no database and so nothing to schedule with.
	schedules    script.ScheduleStore
	runs         script.RunStore
	adminPersona string
	// server is the assembled MCP server, captured at RegisterTool. run_draft
	// opens an in-memory session against it so a draft's platform calls cross
	// the same middleware chain an agent's calls cross.
	server *mcp.Server
}

// New assembles the script layer.
func New(cfg Config) *Handle {
	h := &Handle{store: cfg.Store, runs: cfg.Runs, adminPersona: cfg.AdminPersona}
	if h.store == nil && cfg.DB != nil {
		h.store = scriptstore.New(cfg.DB)
	}
	h.versions, _ = h.store.(script.VersionStore)
	h.schedules, _ = h.store.(script.ScheduleStore)
	return h
}

// resolveEmail returns the calling user's email, or "anonymous" when the call
// carries no identity.
func resolveEmail(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.UserEmail != "" {
		return pc.UserEmail
	}
	return "anonymous"
}

// callerAuthor resolves who is writing a version and the authority they hold
// while writing it.
//
// The roles half is the load-bearing part. Every version records the roles its
// author held, and approving a version binds exactly those roles as the
// authority an approved run presents — so what a script can eventually do is
// capped, at authoring time, by what the person writing it could do. A caller
// with no PlatformContext (a store-level test, an unauthenticated path) authors
// with no roles, which produces a version that cannot be approved into anything
// executable rather than one that quietly inherits someone else's authority.
func callerAuthor(ctx context.Context) script.Author {
	author := script.Author{Email: resolveEmail(ctx), Roles: []string{}}
	if pc := middleware.GetPlatformContext(ctx); pc != nil && len(pc.Roles) > 0 {
		author.Roles = append([]string(nil), pc.Roles...)
	}
	return author
}

// isAdminPersona reports whether the caller holds the admin persona.
func (h *Handle) isAdminPersona(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return false
	}
	return pc.PersonaName == h.adminPersona
}

// resolveScript finds the script a command names. A shared name is globally
// unique; a personal name is unique only within its owner, so a personal lookup
// needs one. Admins may address another owner's personal script by naming that
// owner explicitly. Returns nil, nil when nothing matches.
func (h *Handle) resolveScript(ctx context.Context, name, ownerEmail string) (*script.Script, error) {
	caller := resolveEmail(ctx)
	owner := caller
	if ownerEmail != "" {
		if ownerEmail != caller && !h.isAdminPersona(ctx) {
			return nil, errors.New("you can only address your own personal scripts")
		}
		owner = ownerEmail
	}
	sc, err := h.store.GetPersonal(ctx, owner, name)
	if err != nil {
		return nil, fmt.Errorf("looking up a personal script: %w", err)
	}
	if sc != nil {
		return sc, nil
	}
	shared, err := h.store.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("looking up a shared script: %w", err)
	}
	return shared, nil
}

// readable resolves the script a read command names and checks the caller may
// see it.
func (h *Handle) readable(ctx context.Context, input manageScriptInput) (*script.Script, *mcp.CallToolResult) {
	if input.Name == "" {
		return nil, errorResult("name is required")
	}
	sc, err := h.resolveScript(ctx, input.Name, input.OwnerEmail)
	if err != nil {
		return nil, errorResult(err.Error())
	}
	if sc == nil {
		return nil, errorResult(fmt.Sprintf("script %q not found", input.Name))
	}
	if !h.isAdminPersona(ctx) && !sc.VisibleTo(resolveEmail(ctx), personaName(ctx)) {
		// The same message for "not yours" and "not your persona": naming which
		// one would confirm the script exists to a caller who may not see it.
		return nil, errorResult(fmt.Sprintf("script %q not found", input.Name))
	}
	return sc, nil
}

// personaName returns the caller's resolved persona, or "" when the call
// carries none.
func personaName(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return ""
	}
	return pc.PersonaName
}

// editable resolves the script a mutation names and checks the caller may
// change it. It is the one place that rule is applied, so update, patch, and
// delete cannot drift apart.
func (h *Handle) editable(ctx context.Context, input manageScriptInput) (*script.Script, *mcp.CallToolResult) {
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return nil, errResult
	}
	if h.isAdminPersona(ctx) {
		return sc, nil
	}
	if sc.OwnerEmail != resolveEmail(ctx) {
		return nil, errorResult("you can only change scripts you own")
	}
	if sc.Scope != script.ScopePersonal {
		return nil, errorResult("only admins can change shared (global or persona) scripts")
	}
	return sc, nil
}
