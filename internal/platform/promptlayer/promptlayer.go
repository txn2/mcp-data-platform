// Package promptlayer assembles the prompt subsystem behind one Handle: the
// Postgres-backed prompt store, the file-based tuning prompt manager, the
// name-keyed prompt-metadata registry (promptInfos), and every behavior that
// registers, serves, and manages prompts — the static/workflow/database
// registration path, the per-viewer dynamic-serving path behind the
// prompts/list visibility middleware, and the manage_prompt tool.
//
// Construction takes explicit inputs — an optional *sql.DB (nil leaves the store
// nil; static and workflow prompts still register), the resolved prompts
// directory, the server name/description, the admin persona, the operator prompt
// specs, the built-in-prompt disable map, and the toolkit registry — so the
// subsystem is constructible and testable without a caller assembling it. It
// imports pkg/prompt, pkg/tuning, pkg/registry, pkg/embedding, pkg/middleware,
// pkg/portal, and the MCP SDK, never the platform package.
//
// The MCP server is NOT captured at construction: the store and metadata must
// exist early (other subsystems read the store before the server is created),
// but the AddPrompt/AddTool registration happens later, so RegisterPlatformPrompts
// and RegisterTool take the *mcp.Server per call. Two collaborators are bound
// after construction because they are assembled later than the prompt store: the
// embedding provider (SetEmbedder — powers manage_prompt semantic ranking; nil
// falls back to lexical) and the portal share lister (SetShareStore — resolves
// prompts shared directly with a caller; nil serves no shared prompts). Both
// serving paths nil-check their collaborator, so a Handle with neither set still
// serves global/persona/personal prompts.
package promptlayer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer/notifystore"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	promptpostgres "github.com/txn2/mcp-data-platform/pkg/prompt/postgres"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/tuning"
)

// logKeyError is the slog key for error values in log messages.
const logKeyError = "error"

// ToolkitRegistry is the read-only slice of the toolkit registry the prompt
// layer needs: enabled-kind checks for capability bullets and workflow gating,
// and the full toolkit list to collect prompt metadata from PromptDescriber
// toolkits. The concrete *registry.Registry satisfies it.
type ToolkitRegistry interface {
	GetByKind(kind string) []registry.Toolkit
	All() []registry.Toolkit
}

// ShareLister looks up the prompts shared directly with a caller. The portal
// share store satisfies it; it is bound via SetShareStore once the portal layer
// exists. A nil lister disables shared-prompt serving.
type ShareLister interface {
	ListSharedPromptsWithUser(ctx context.Context, userID, email string) ([]portal.SharedPromptRef, error)
}

// Config carries the resolved values the owner needs to assemble the prompt
// layer. The caller translates its own config into this shape so this package
// stays free of the platform's config types.
type Config struct {
	// DB backs the prompt store; nil leaves the store nil (static and workflow
	// prompts still register, but database prompts and manage_prompt are off).
	// Ignored when Store is set.
	DB *sql.DB
	// Store, when non-nil, is used as the prompt store directly instead of
	// building a Postgres store from DB. Lets a caller (or a test) supply an
	// already-assembled store; production passes DB and leaves this nil.
	Store prompt.Store
	// PromptsDir is the directory the tuning prompt manager loads file prompts
	// from (empty is valid: the manager loads nothing).
	PromptsDir string
	// ServerName titles the auto-generated platform-overview prompt.
	ServerName string
	// ServerDescription supplies that prompt's body text. It is a function
	// because the description is admin-editable and database-backed, so the
	// text must be resolved when the prompt is served rather than baked in
	// at registration. Nil skips the prompt entirely; the caller passes nil
	// when no description exists and none can appear later.
	ServerDescription func(ctx context.Context) string
	// AdminPersona is the persona name that grants admin authority over prompts
	// at every scope; matched against the caller's persona in each command.
	AdminPersona string
	// OperatorPrompts are the operator-configured static prompts to register.
	OperatorPrompts []PromptSpec
	// BuiltinPrompts disables named built-in workflow prompts (name → enabled);
	// a name mapped to false is skipped. Nil enables every built-in.
	BuiltinPrompts map[string]bool
	// Registry is the toolkit registry read for capability bullets, workflow
	// gating, and toolkit prompt metadata.
	Registry ToolkitRegistry
}

// Handle owns the assembled prompt layer: the prompt store, the tuning prompt
// manager, and the name-keyed prompt-metadata list, plus the registration,
// dynamic-serving, and manage_prompt behaviors. Store exposes the backing store
// the caller surfaces and hands to the search federation; RegisterPlatformPrompts
// and RegisterTool take the *mcp.Server per call; ListVisible / GetByName are the
// prompts/list visibility callbacks the caller wires into the middleware chain;
// AllPromptInfos / RegisterRuntimePrompt / UnregisterRuntimePrompt back the admin
// and portal prompt REST handlers. All accessors are nil-safe.
type Handle struct {
	store         prompt.Store
	promptManager *tuning.PromptManager
	registry      ToolkitRegistry

	serverName        string
	serverDescription func(ctx context.Context) string
	adminPersona      string
	operatorPrompts   []PromptSpec
	builtinPrompts    map[string]bool

	// Bound after construction (assembled later than the prompt store).
	embedder   embedding.Provider
	shareStore ShareLister

	// attachments resolves a prompt's attached reference material for the
	// caller (#1013). Bound after construction because it needs the resource
	// store and blob backend, which are assembled later; nil serves every
	// prompt without materials, which is exactly the behavior a deployment
	// with managed resources disabled should get.
	attachments *attachserve.Resolver

	// auditLogger receives prompt_serve audit events on every successful
	// database-prompt serve (prompts/get, manage_prompt use); usage reads the
	// aggregation back for manage_prompt get. Both bound after construction
	// (the audit layer is assembled later); nil disables the respective path.
	auditLogger middleware.AuditLogger
	usage       prompt.UsageReader

	// listChanged holds the prompts/list_changed notifier, bound after
	// construction once the session broadcaster exists. Read atomically per
	// write by the notifying store wrapper; nil until SetListChangedNotifier.
	listChanged atomicNotifier

	promptInfosMu sync.RWMutex
	promptInfos   []registry.PromptInfo
}

// New assembles the prompt layer. It always returns a non-nil Handle: prompts
// are a first-class feature even without a database (static and workflow prompts
// register and serve), so a no-DB deployment gets a Handle with a nil store and
// every store-backed path (database prompts, manage_prompt, search) degrades to
// a no-op. The tuning prompt manager is built here from the configured prompts
// directory; the caller starts it with LoadPrompts.
func New(cfg Config) *Handle {
	h := &Handle{
		promptManager:     tuning.NewPromptManager(tuning.PromptConfig{PromptsDir: cfg.PromptsDir}),
		registry:          cfg.Registry,
		serverName:        cfg.ServerName,
		serverDescription: cfg.ServerDescription,
		adminPersona:      cfg.AdminPersona,
		operatorPrompts:   cfg.OperatorPrompts,
		builtinPrompts:    cfg.BuiltinPrompts,
	}
	var base prompt.Store
	switch {
	case cfg.Store != nil:
		base = cfg.Store
	case cfg.DB != nil:
		base = promptpostgres.New(cfg.DB)
		slog.Info("prompt store: postgres")
	}
	if base != nil {
		// Wrap the backing store so every write path (manage_prompt tool,
		// admin/portal REST, knowledge add_prompt) fires prompts/list_changed
		// through the one shared instance — no per-handler emission to keep in
		// sync. The wrapper preserves the store's capability extensions
		// (search, versioning), so version writes that change what is served
		// fire list_changed like every other write; the notifier is bound
		// later via SetListChangedNotifier.
		h.store = notifystore.Wrap(base, h.notifyListChanged, h.guardAttachmentScope)
	}
	return h
}

// SetAttachmentResolver binds the resolver that turns a prompt's attached
// resource links into served material. Called once the managed-resource layer
// is assembled; nil (or never calling this) serves prompts without attachments.
func (h *Handle) SetAttachmentResolver(r *attachserve.Resolver) {
	h.attachments = r
}

// SetAuditLogger binds the audit logger that receives prompt_serve events.
// Called once the audit layer is assembled; nil (or never calling this)
// disables serve-event emission, and with it usage stats.
func (h *Handle) SetAuditLogger(l middleware.AuditLogger) {
	h.auditLogger = l
}

// SetUsageReader binds the audit-backed usage aggregation surfaced on
// manage_prompt get. Called once the audit store is assembled; nil (or never
// calling this) leaves usage fields unpopulated.
func (h *Handle) SetUsageReader(u prompt.UsageReader) {
	h.usage = u
}

// SetEmbedder binds the embedding provider that powers manage_prompt semantic
// ranking. Called once the provider is assembled; a nil provider (or never
// calling this) leaves ranking on the lexical fallback.
func (h *Handle) SetEmbedder(e embedding.Provider) {
	h.embedder = e
}

// SetShareStore binds the portal share lister used to resolve prompts shared
// directly with a caller. Called once the portal layer exists; a nil lister (or
// never calling this) serves no shared prompts.
func (h *Handle) SetShareStore(s ShareLister) {
	h.shareStore = s
}

// Store returns the backing prompt store, or nil on a nil Handle or a no-DB
// deployment. The caller surfaces it and hands it to the search federation.
func (h *Handle) Store() prompt.Store {
	if h == nil {
		return nil
	}
	return h.store
}

// LoadPrompts loads the file-based tuning prompts from the configured directory.
// Called once at startup.
func (h *Handle) LoadPrompts() error {
	if err := h.promptManager.LoadPrompts(); err != nil {
		return fmt.Errorf("promptlayer: loading file prompts: %w", err)
	}
	return nil
}

// PromptCreatorAdapter adapts the Handle to the knowledge toolkit's PromptCreator
// interface (Create + RegisterRuntimePrompt) for the add_prompt change type,
// without this package importing the knowledge toolkit.
type PromptCreatorAdapter struct {
	h *Handle
}

// PromptCreator returns an adapter satisfying the knowledge toolkit's
// PromptCreator interface over this handle, or nil on a no-DB deployment (no
// store to create into).
func (h *Handle) PromptCreator() *PromptCreatorAdapter {
	if h == nil || h.store == nil {
		return nil
	}
	return &PromptCreatorAdapter{h: h}
}

// Create persists a new prompt through the backing store.
func (c *PromptCreatorAdapter) Create(ctx context.Context, p *prompt.Prompt) error {
	if err := c.h.store.Create(ctx, p); err != nil {
		return fmt.Errorf("prompt store create: %w", err)
	}
	return nil
}

// RegisterRuntimePrompt records the prompt's metadata for admin listing.
func (c *PromptCreatorAdapter) RegisterRuntimePrompt(p *prompt.Prompt) {
	c.h.RegisterRuntimePrompt(p)
}
