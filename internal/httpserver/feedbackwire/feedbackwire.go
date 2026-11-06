// Package feedbackwire assembles who can open a feedback thread's target
// (#2512) for the people a message names or a thread involves, and the audit
// logger a portal edit or delete of a message is recorded through (#2514).
//
// The decision is internal/portal/threadreach over the portal's authorization
// core (internal/portal/access) built from the same stores and administrator
// rule the portal builds its own from, so a mention, a reply's notifications
// and a thread route admit the same people. A named person's roles come from
// the known-users directory.
package feedbackwire

import (
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/threadreach"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Sources is what the assembly reads from the platform.
type Sources struct {
	// Directory holds the roles each person last signed in with. nil
	// judges every named person with no roles.
	Directory user.Store
	// Audit records a portal edit or delete. nil records nothing.
	Audit middleware.AuditLogger
	// HasDatabase says mentions are available: they are recorded on thread
	// events, which need one.
	HasDatabase bool
}

// Wire builds the decision, hands it to the mention resolver the feedback
// routes use and the audit logger to the portal, and returns it for the
// notifier and the MCP feedback tool. It reads the persona resolver, so it
// runs after the portal's optional dependencies are set.
func Wire(deps *portal.Deps, src Sources) *threadreach.Reach {
	cfg := threadreach.Config{
		Access: access.New(access.Config{
			Assets: deps.AssetStore, Collections: deps.CollectionStore, Shares: deps.ShareStore,
			Prompts: deps.PromptStore, AdminRoles: deps.AdminRoles, AdminRule: deps.AdminRule,
		}),
		Assets:         deps.AssetStore,
		Collections:    deps.CollectionStore,
		KnowledgePages: deps.KnowledgePageStore,
	}
	if deps.PromptStore != nil {
		cfg.Prompts = deps.PromptStore
	}
	if src.Directory != nil {
		cfg.Directory = src.Directory
	}
	if resolve := deps.PersonaResolver; resolve != nil {
		cfg.PersonaName = func(roles []string) string {
			if info := resolve(roles); info != nil {
				return info.Name
			}
			return ""
		}
	}
	reach := threadreach.New(cfg)
	if src.HasDatabase {
		deps.MentionResolver = mention.NewService(reach)
	}
	if src.Audit != nil {
		deps.AuditLogger = src.Audit
	}
	return reach
}
