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

	"github.com/txn2/mcp-data-platform/internal/platform/scriptindex"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
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
	// AdminPersona is the persona name that grants authority over every
	// script; matched against the caller's persona in each command.
	AdminPersona string
	// PortalURL is the deployment's public portal address, used by show_scripts
	// to name where the script pages are. Empty leaves the tool registered and
	// linkless: a deployment that has not been told its own address cannot be
	// given one by guessing.
	PortalURL string
	// Destinations is the deployment's configured bucket destinations, which a
	// draft run resolves platform.export names against exactly as a platform
	// run does.
	Destinations []script.Destination
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
	schedules script.ScheduleStore
	// states is the same store narrowed to its state contract (#1537), nil
	// where the deployment has no database and so nothing to carry between
	// runs.
	states       script.StateStore
	runs         script.RunStore
	adminPersona string
	// portalURL is the public portal address show_scripts points the human at,
	// empty when the deployment has not been configured with one.
	portalURL string
	// server is the assembled MCP server, captured at RegisterTool. run_draft
	// opens an in-memory session against it so a draft's platform calls cross
	// the same middleware chain an agent's calls cross.
	server *mcp.Server
	// destinations is the configured bucket destination set draft runs resolve
	// export names against.
	destinations []script.Destination
	// indexProducer is the write-path index-job producer the Postgres script
	// store was built with, so a created or re-described script enters ranked
	// search without waiting for the reconciler (#1370). Nil when the layer was
	// handed a store rather than building one, since that store's write path is
	// the caller's to wire.
	indexProducer *indexjobs.Producer
}

// New assembles the script layer.
func New(cfg Config) *Handle {
	h := &Handle{
		store: cfg.Store, runs: cfg.Runs, adminPersona: cfg.AdminPersona,
		portalURL: cfg.PortalURL, destinations: cfg.Destinations,
	}
	if h.store == nil && cfg.DB != nil {
		h.indexProducer = indexjobs.NewProducer(scriptindex.SourceKind)
		h.store = scriptstore.New(cfg.DB, indexjobs.WithProducer(h.indexProducer))
	}
	h.versions, _ = h.store.(script.VersionStore)
	h.schedules, _ = h.store.(script.ScheduleStore)
	h.states, _ = h.store.(script.StateStore)
	return h
}

// IndexProducer returns the write-path index-job producer behind the managed-
// script store, or nil on a deployment with no database. The composition root
// hands it to the index queue, which binds it once the scripts consumer is
// registered; until then, and forever where no worker runs, NotifyWrite is a
// no-op and the reconciler is the only route to the index.
func (h *Handle) IndexProducer() *indexjobs.Producer {
	if h == nil {
		return nil
	}
	return h.indexProducer
}

// resolveEmail returns the identity a script is owned by and compared against:
// the caller's email, their user id when the credential carries no email, and
// "anonymous" only when there is no identity at all.
//
// The user-id fallback is load-bearing rather than cosmetic. An OIDC token
// without an email claim leaves UserEmail empty, so falling straight through to
// the "anonymous" sentinel would give every such caller the SAME owner string,
// and a script is exactly as private as that comparison is specific:
// two different people would read, edit, and run each other's scripts. A user
// id is distinct per caller wherever a credential identifies one at all.
//
// "anonymous" therefore means what it says — no identity was presented — which
// is the single-caller deployment with no authenticator configured, where one
// owner string is the whole population. A script left owned by that string in a
// deployment that later starts identifying its callers matches nobody but an
// admin, which is the fail-closed answer for a record whose owner cannot be
// established.
func resolveEmail(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.UserEmail != "" {
		return pc.UserEmail
	}
	if pc != nil && pc.UserID != "" {
		return pc.UserID
	}
	return "anonymous"
}

// callerAuthor resolves who is writing a version and the authority they hold
// while writing it.
//
// The roles half is the load-bearing part. Every version records the roles its
// author held, and a run of that version presents exactly those roles — so
// what a script can do unattended is capped, at authoring time, by what the
// person writing it could do. A caller with no PlatformContext (a store-level
// test, an unauthenticated path) authors with no roles, which produces a
// version whose runs resolve to the deny-all persona rather than one that
// quietly inherits someone else's authority.
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

// resolveScript finds the script a command names. A name is unique only within
// its owner, so every lookup names one: the caller, or the person an admin
// addressed explicitly. Returns nil, nil when nothing matches.
func (h *Handle) resolveScript(ctx context.Context, name, ownerEmail string) (*script.Script, error) {
	caller := resolveEmail(ctx)
	owner := caller
	if ownerEmail != "" {
		if ownerEmail != caller && !h.isAdminPersona(ctx) {
			return nil, errors.New("you can only address your own scripts")
		}
		owner = ownerEmail
	}
	sc, err := h.store.GetByName(ctx, owner, name)
	if err != nil {
		return nil, fmt.Errorf("looking up a script: %w", err)
	}
	return sc, nil
}

// refuseReentrantRun refuses a run asked for from inside a run, naming what was
// asked for.
//
// This is a runaway-work guard, not an authorization rule. A script may call
// every tool its author's persona authorizes (#1419), and run_script is one of
// them — but a script that starts a run can start a script that starts a run,
// and while Starlark has no while and no recursion, so a single run cannot
// loop, a cycle ACROSS runs has nothing to stop it. It would also deadlock on
// the way there: a worker executes one run at a time per replica
// (internal/platform/scriptexec/worker.go), so a script waiting on a run it
// queued is waiting on the worker it is itself occupying.
//
// The signal is PlatformContext.Source, which the run layer sets to
// SourceScript for every call a run makes, so the guard covers a platform run
// and a draft alike and needs nothing threaded through the interpreter.
func refuseReentrantRun(ctx context.Context, what string) *mcp.CallToolResult {
	if !insideRun(ctx) {
		return nil
	}
	return errorResult(what + " cannot be called from inside a script run: a run executes one at a time, " +
		"so a script waiting on a run it started would wait on the worker running it. " +
		"Do the work in this script, or give the second script its own schedule.")
}

// refuseScriptAuthoring refuses a command that would author, edit, delete or
// schedule a script when it was issued from inside a run. See
// scriptWritingCommands for why the surface is closed to a run rather than
// merely the two execution verbs.
func refuseScriptAuthoring(ctx context.Context, what string) *mcp.CallToolResult {
	if !insideRun(ctx) {
		return nil
	}
	return errorResult(what + " cannot be called from inside a script run: a run may read the script " +
		"surface but never change what exists or what will execute. A script that could write a script " +
		"could schedule unbounded work, and a script that could edit itself would capture the roles it " +
		"is running with as a new version's authority. Make the change from your own session.")
}

// insideRun reports whether the current call is one a managed-script run made.
// The signal is PlatformContext.Source, which the run layer sets to
// SourceScript for every call a run makes, so it covers a platform run and a
// draft alike and needs nothing threaded through the interpreter.
func insideRun(ctx context.Context) bool {
	pc := middleware.GetPlatformContext(ctx)
	return pc != nil && pc.Source == middleware.SourceScript
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
	if !h.isAdminPersona(ctx) && !sc.OwnedBy(resolveEmail(ctx)) {
		// "Not found" rather than "not yours": naming the difference would
		// confirm the script exists to a caller who may not see it.
		return nil, errorResult(fmt.Sprintf("script %q not found", input.Name))
	}
	return sc, nil
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
	if !sc.OwnedBy(resolveEmail(ctx)) {
		return nil, errorResult("you can only change scripts you own")
	}
	return sc, nil
}
