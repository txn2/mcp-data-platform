// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Prompt and toolkit kind constants.
const (
	autoPromptName = "platform-overview"
	kindDataHub    = "datahub"
	kindTrino      = "trino"
	kindS3         = "s3"
	kindPortal     = "portal"
	kindKnowledge  = "knowledge"
	kindMemory     = "memory"
	kindMCP        = "mcp"
	kindAPI        = "api"
	// promptArgTopic is the argument name shared across workflow prompts
	// that take a free-form subject ("explore", "create-dashboard", etc.).
	promptArgTopic = "topic"
	// promptRoleUser is the MCP message role for user-authored content.
	// Untyped so it converts to mcp.Role at the call site.
	promptRoleUser = "user"
)

// registerPlatformPrompts registers platform-level prompts from config.
// It first registers the auto-generated platform overview prompt (if applicable),
// then registers operator-configured prompts, then workflow prompts,
// then database-stored prompts.
func (p *Platform) registerPlatformPrompts() {
	p.registerAutoPrompt()
	for _, promptCfg := range p.config.Server.Prompts {
		p.registerPromptWithCategory(promptCfg, "custom")
	}
	p.registerWorkflowPrompts()
	p.registerDatabasePrompts()
}

// registerAutoPrompt registers the auto-generated "platform-overview" prompt when
// server.description is non-empty. It is skipped if an operator-configured prompt
// already uses the name "platform-overview".
//
// The content is built dynamically based on enabled toolkits, listing what the
// user can do with this platform.
func (p *Platform) registerAutoPrompt() {
	if p.config.Server.Description == "" {
		return
	}

	// Skip if operator has already defined a prompt with this name.
	if p.isOperatorPrompt(autoPromptName) {
		return
	}

	content := p.buildDynamicOverviewContent()

	p.mcpServer.AddPrompt(&mcp.Prompt{
		Name:        autoPromptName,
		Title:       p.config.Server.Name,
		Description: "Overview of this data platform — what it covers and how to use it",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return buildPromptResult(content), nil
	})

	// platform-overview is auto-invoked; it is not included in promptInfos
	// because copy-to-clipboard makes no sense for it.
}

// buildDynamicOverviewContent builds the platform overview content dynamically
// based on the server description and enabled toolkits.
func (p *Platform) buildDynamicOverviewContent() string {
	var b strings.Builder
	b.WriteString(p.config.Server.Description)
	b.WriteString("\n\n")

	capabilities := p.collectCapabilityBullets()
	if len(capabilities) > 0 {
		b.WriteString("With this platform you can:\n")
		for _, bullet := range capabilities {
			b.WriteString("- ")
			b.WriteString(bullet)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Call `platform_info` for full technical details.")
	return b.String()
}

// capabilityEntry maps a toolkit availability predicate to its user-facing description.
type capabilityEntry struct {
	check func(has map[string]bool) bool
	text  string
}

// capabilityTable defines the ordered set of capability bullets shown in the
// platform overview. Each entry specifies which toolkits must be present and
// the resulting description. Extracted to reduce cyclomatic complexity.
func capabilityTable() []capabilityEntry {
	return []capabilityEntry{
		{check: func(h map[string]bool) bool { return h[kindDataHub] }, text: "Explore available data and trace lineage through the data catalog"},
		{check: func(h map[string]bool) bool { return h[kindTrino] }, text: "Query data using SQL across connected databases"},
		{check: func(h map[string]bool) bool { return h[kindS3] }, text: "Browse and retrieve files from object storage"},
		{check: func(h map[string]bool) bool { return h[kindPortal] }, text: "Save artifacts (dashboards, reports, charts) as viewable, shareable assets"},
		{check: func(h map[string]bool) bool { return h[kindKnowledge] }, text: "Capture domain knowledge and insights to improve the data catalog. Knowledge is captured automatically from conversations, not just when asked"},
		{check: func(h map[string]bool) bool { return h[kindMemory] }, text: "Remember corrections, preferences, and context across sessions. Agents store what they learn and apply it in future conversations"},
		{check: func(h map[string]bool) bool { return h[kindDataHub] && h[kindTrino] }, text: "Generate reports by discovering data and querying it"},
		{check: func(h map[string]bool) bool { return h[kindDataHub] && h[kindTrino] && h[kindPortal] }, text: "Create interactive dashboards and save them for later viewing"},
	}
}

// collectCapabilityBullets returns human-readable capability descriptions
// based on which toolkits are enabled.
func (p *Platform) collectCapabilityBullets() []string {
	has := map[string]bool{
		kindDataHub:   len(p.toolkitRegistry.GetByKind(kindDataHub)) > 0,
		kindTrino:     len(p.toolkitRegistry.GetByKind(kindTrino)) > 0,
		kindS3:        len(p.toolkitRegistry.GetByKind(kindS3)) > 0,
		kindPortal:    len(p.toolkitRegistry.GetByKind(kindPortal)) > 0,
		kindKnowledge: len(p.toolkitRegistry.GetByKind(kindKnowledge)) > 0,
		kindMemory:    len(p.toolkitRegistry.GetByKind(kindMemory)) > 0,
	}

	var caps []string
	for _, entry := range capabilityTable() {
		if entry.check(has) {
			caps = append(caps, entry.text)
		}
	}
	return caps
}

// registerPromptWithCategory registers a single prompt with the MCP server,
// supporting argument substitution in content. The category is stored in
// prompt metadata for frontend grouping (e.g., "workflow", "custom", "toolkit").
func (p *Platform) registerPromptWithCategory(cfg PromptConfig, category string) {
	promptContent := cfg.Content

	// Build MCP prompt arguments
	mcpArgs := make([]*mcp.PromptArgument, 0, len(cfg.Arguments))
	for _, arg := range cfg.Arguments {
		mcpArgs = append(mcpArgs, &mcp.PromptArgument{
			Name:        arg.Name,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}

	p.mcpServer.AddPrompt(&mcp.Prompt{
		Name:        cfg.Name,
		Description: cfg.Description,
		Arguments:   mcpArgs,
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		resolved := substituteArgs(promptContent, req.Params.Arguments)
		return buildPromptResult(resolved), nil
	})

	// Collect metadata
	info := registry.PromptInfo{
		Name:        cfg.Name,
		Description: cfg.Description,
		Category:    category,
		Content:     cfg.Content,
	}
	for _, arg := range cfg.Arguments {
		info.Arguments = append(info.Arguments, registry.PromptArgumentInfo{
			Name:        arg.Name,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}
	p.promptInfosMu.Lock()
	p.promptInfos = append(p.promptInfos, info)
	p.promptInfosMu.Unlock()
}

// substituteArgs replaces {arg_name} placeholders in content with values from
// the arguments map. Unresolved placeholders are left as-is. Keys are sorted
// to ensure deterministic output when values contain other argument placeholders.
// substituteArgs replaces both {name} and {{name}} placeholders with values
// from args. Double-brace is processed first so that a {{name}} placeholder
// is not accidentally consumed by a {name} replacement of a substring.
func substituteArgs(content string, args map[string]string) string {
	if len(args) == 0 {
		return content
	}
	keys := make([]string, 0, len(args))
	for name := range args {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	result := content
	for _, name := range keys {
		result = strings.ReplaceAll(result, "{{"+name+"}}", args[name])
		result = strings.ReplaceAll(result, "{"+name+"}", args[name])
	}
	return result
}

// workflowPrompt defines a platform-level workflow prompt with its required toolkits.
type workflowPrompt struct {
	config        PromptConfig
	requiredKinds []string
}

// promptExploreAvailableData is the canonical name of the data-exploration
// workflow prompt. Used by the prompt registration code and referenced by
// the disable-prompt allowlist in tests.
const promptExploreAvailableData = "explore-available-data"

// workflowPrompts returns the set of platform-level workflow prompts.
func workflowPrompts() []workflowPrompt {
	return []workflowPrompt{
		{
			config: PromptConfig{
				Name:        promptExploreAvailableData,
				Description: "Discover what data is available about a topic",
				Content: `Explore what data is available about {topic}.

1. Search the data catalog for datasets related to this topic
2. Present relevant datasets with descriptions, ownership, and quality scores
3. Highlight data products that group related datasets
4. Note any data quality concerns or deprecation warnings`,
				Arguments: []PromptArgumentConfig{
					{Name: promptArgTopic, Description: "What topic or subject area?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub},
		},
		{
			config: PromptConfig{
				Name:        "create-interactive-dashboard",
				Description: "Discover data, build a visualization, and save it as a shareable asset",
				Content: `Create an interactive dashboard about {topic}.

1. Explore what data is available about this topic
2. Query the most relevant datasets
3. Build an interactive visualization with key metrics and trends
4. Save it as an artifact I can view and share`,
				Arguments: []PromptArgumentConfig{
					{Name: promptArgTopic, Description: "What should the dashboard visualize?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub, kindTrino, kindPortal},
		},
		{
			config: PromptConfig{
				Name:        "create-a-report",
				Description: "Analyze data and produce a structured Markdown report",
				Content: `Generate a comprehensive report about {topic}.

1. Discover relevant datasets in the data catalog
2. Query and analyze the data for key findings
3. Produce a well-structured Markdown report with tables, metrics, and insights
4. Summarize the key takeaways`,
				Arguments: []PromptArgumentConfig{
					{Name: promptArgTopic, Description: "What should the report cover?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub, kindTrino},
		},
		{
			config: PromptConfig{
				Name:        "trace-data-lineage",
				Description: "Trace where data comes from and what depends on it",
				Content: `Trace the data lineage for {dataset}.

1. Identify the upstream sources that feed this data
2. Map the downstream consumers that depend on it
3. Show column-level lineage where available
4. Highlight any transformation steps in the pipeline`,
				Arguments: []PromptArgumentConfig{
					{Name: "dataset", Description: "Which dataset or column to trace?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub},
		},
	}
}

// registerWorkflowPrompts registers platform-level workflow prompts
// conditional on the required toolkits being available.
// Skips any prompt whose name matches an operator-configured prompt or that
// has been explicitly disabled via server.builtin_prompts config.
func (p *Platform) registerWorkflowPrompts() {
	for _, wp := range workflowPrompts() {
		// Skip if explicitly disabled in config
		if p.isBuiltinDisabled(wp.config.Name) {
			continue
		}

		// Skip if operator already defined this prompt
		if p.isOperatorPrompt(wp.config.Name) {
			continue
		}

		// Check all required toolkit kinds are present
		if !p.hasAllToolkitKinds(wp.requiredKinds) {
			continue
		}

		p.registerPromptWithCategory(wp.config, "workflow")
	}
}

// isBuiltinDisabled checks if a built-in prompt has been explicitly disabled
// via server.builtin_prompts config. If the map is nil or the key is absent,
// the prompt is enabled by default.
func (p *Platform) isBuiltinDisabled(name string) bool {
	if p.config.Server.BuiltinPrompts == nil {
		return false
	}
	enabled, exists := p.config.Server.BuiltinPrompts[name]
	return exists && !enabled
}

// isOperatorPrompt checks if a prompt name is already defined in operator config.
func (p *Platform) isOperatorPrompt(name string) bool {
	for _, cfg := range p.config.Server.Prompts {
		if cfg.Name == name {
			return true
		}
	}
	return false
}

// hasAllToolkitKinds checks that every kind in the list has at least one registered toolkit.
func (p *Platform) hasAllToolkitKinds(kinds []string) bool {
	for _, kind := range kinds {
		if len(p.toolkitRegistry.GetByKind(kind)) == 0 {
			return false
		}
	}
	return true
}

// collectToolkitPromptInfos gathers prompt metadata from toolkits that implement PromptDescriber.
func (p *Platform) collectToolkitPromptInfos() []registry.PromptInfo {
	var infos []registry.PromptInfo
	for _, tk := range p.toolkitRegistry.All() {
		if pd, ok := tk.(registry.PromptDescriber); ok {
			infos = append(infos, pd.PromptInfos()...)
		}
	}
	return infos
}

// AllPromptInfos returns all prompt metadata (platform + toolkit).
// Exported for the admin API to surface system prompts.
func (p *Platform) AllPromptInfos() []registry.PromptInfo {
	tkInfos := p.collectToolkitPromptInfos()
	p.promptInfosMu.RLock()
	all := make([]registry.PromptInfo, 0, len(p.promptInfos)+len(tkInfos))
	all = append(all, p.promptInfos...)
	p.promptInfosMu.RUnlock()
	all = append(all, tkInfos...)
	return all
}

// registerDatabasePrompts loads enabled prompts from the database and registers
// them with the MCP server. Called during startup after the prompt store is initialized.
func (p *Platform) registerDatabasePrompts() {
	if p.promptStore == nil {
		return
	}

	enabled := true
	prompts, err := p.promptStore.List(context.Background(), prompt.ListFilter{Enabled: &enabled})
	if err != nil {
		slog.Warn("failed to load prompts from database", logKeyError, err)
		return
	}

	for i := range prompts {
		p.registerDatabasePrompt(&prompts[i])
	}
	if len(prompts) > 0 {
		slog.Info("loaded prompts from database", "count", len(prompts))
	}
}

// registerDatabasePrompt records a database prompt's metadata for admin
// listing. Database prompts are NOT placed in the shared static MCP registry:
// the registry is keyed by name and cannot represent per-viewer names (the
// scope prefix an analyst sees differs from a global viewer) or two users'
// same-named personal prompts. They are served per-caller by the
// prompt-visibility middleware, which lists and resolves them from the
// database with a scope prefix (global-, <persona>-, personal-) computed at
// serve time.
func (p *Platform) registerDatabasePrompt(pr *prompt.Prompt) {
	info := registry.PromptInfo{
		Name:        pr.Name,
		Description: pr.Description,
		Category:    pr.Scope,
		Content:     pr.Content,
	}
	for _, arg := range pr.Arguments {
		info.Arguments = append(info.Arguments, registry.PromptArgumentInfo{
			Name:        arg.Name,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}
	p.promptInfosMu.Lock()
	p.promptInfos = append(p.promptInfos, info)
	p.promptInfosMu.Unlock()
}

// toMCPPromptArgs maps prompt arguments to their MCP descriptors.
func toMCPPromptArgs(args []prompt.Argument) []*mcp.PromptArgument {
	out := make([]*mcp.PromptArgument, 0, len(args))
	for _, a := range args {
		out = append(out, &mcp.PromptArgument{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
		})
	}
	return out
}

// Scope prefixes for the dynamic prompt names presented to MCP clients. The
// prefix tells the agent the scope and is computed per-viewer at serve time;
// the database stores only the bare name. "personal" and "global" are reserved
// so prefix-stripping on prompts/get is unambiguous.
const (
	promptPrefixPersonal = "personal-"
	promptPrefixGlobal   = "global-"
)

// promptDescriptor builds an MCP prompt descriptor under a presented (prefixed)
// name from a stored prompt.
func promptDescriptor(presentedName string, pr *prompt.Prompt) *mcp.Prompt {
	return &mcp.Prompt{
		Name:        presentedName,
		Description: pr.Description,
		Arguments:   toMCPPromptArgs(pr.Arguments),
	}
}

// listVisiblePrompts returns the caller's visible database prompts as MCP
// descriptors with their scope prefix: global-<name> for globals,
// <persona>-<name> for each persona the caller belongs to, and personal-<name>
// for the caller's own. A persona prompt shared with several personas appears
// once per persona the caller is in.
func (p *Platform) listVisiblePrompts(ctx context.Context, email string, personas []string) []*mcp.Prompt {
	if p.promptStore == nil {
		return nil
	}
	out := p.listScopedDescriptors(ctx, prompt.ListFilter{Scope: prompt.ScopeGlobal}, promptPrefixGlobal)
	out = append(out, p.listPersonaDescriptors(ctx, personas)...)
	if email != "" {
		out = append(out, p.listScopedDescriptors(ctx, prompt.ListFilter{Scope: prompt.ScopePersonal, OwnerEmail: email}, promptPrefixPersonal)...)
	}
	return out
}

// listScopedDescriptors lists enabled prompts matching the filter and presents
// each under a fixed scope prefix (for global and personal scopes).
func (p *Platform) listScopedDescriptors(ctx context.Context, filter prompt.ListFilter, prefix string) []*mcp.Prompt {
	enabled := true
	filter.Enabled = &enabled
	prompts, err := p.promptStore.List(ctx, filter)
	if err != nil {
		slog.Warn("failed to list prompts", logKeyError, err, "scope", filter.Scope)
		return nil
	}
	out := make([]*mcp.Prompt, 0, len(prompts))
	for i := range prompts {
		out = append(out, promptDescriptor(prefix+prompts[i].Name, &prompts[i]))
	}
	return out
}

// listPersonaDescriptors lists the caller's persona prompts, presenting each
// once per persona the caller belongs to (the prefix is the persona name).
func (p *Platform) listPersonaDescriptors(ctx context.Context, personas []string) []*mcp.Prompt {
	if len(personas) == 0 {
		return nil
	}
	enabled := true
	personaPrompts, err := p.promptStore.List(ctx, prompt.ListFilter{
		Scope: prompt.ScopePersona, Personas: personas, Enabled: &enabled,
	})
	if err != nil {
		slog.Warn("failed to list persona prompts", logKeyError, err)
		return nil
	}
	var out []*mcp.Prompt
	for i := range personaPrompts {
		for _, persona := range personas {
			if slices.Contains(personaPrompts[i].Personas, persona) {
				out = append(out, promptDescriptor(persona+"-"+personaPrompts[i].Name, &personaPrompts[i]))
			}
		}
	}
	return out
}

// getDynamicPrompt resolves a prefixed prompt name to the caller's visible
// database prompt and renders it for prompts/get. It strips the scope prefix to
// the bare stored name: personal-/global- are reserved tokens; a persona prefix
// must be one of the caller's personas, and the target prompt must actually be
// shared with that persona. Returns (nil, false) when no such visible prompt
// exists.
func (p *Platform) getDynamicPrompt(ctx context.Context, email string, personas []string, name string, args map[string]string) (*mcp.GetPromptResult, bool) {
	if p.promptStore == nil {
		return nil, false
	}
	if bare, ok := strings.CutPrefix(name, promptPrefixPersonal); ok {
		return p.getOwnedPersonalPrompt(ctx, email, bare, args)
	}
	if bare, ok := strings.CutPrefix(name, promptPrefixGlobal); ok {
		return p.getGlobalPrompt(ctx, bare, args)
	}
	return p.getPersonaPrompt(ctx, personas, name, args)
}

// getOwnedPersonalPrompt renders the caller's own personal prompt of the bare name.
func (p *Platform) getOwnedPersonalPrompt(ctx context.Context, email, bare string, args map[string]string) (*mcp.GetPromptResult, bool) {
	if email == "" {
		return nil, false
	}
	pr, err := p.promptStore.GetPersonal(ctx, email, bare)
	if err != nil || pr == nil || !pr.Enabled {
		return nil, false
	}
	return p.renderPrompt(pr, args)
}

// getGlobalPrompt renders the global prompt of the bare name.
func (p *Platform) getGlobalPrompt(ctx context.Context, bare string, args map[string]string) (*mcp.GetPromptResult, bool) {
	pr, err := p.promptStore.Get(ctx, bare)
	if err != nil || pr == nil || !pr.Enabled || pr.Scope != prompt.ScopeGlobal {
		return nil, false
	}
	return p.renderPrompt(pr, args)
}

// getPersonaPrompt resolves a <persona>-<name> prompt for a caller who belongs
// to that persona and is shared the target prompt.
func (p *Platform) getPersonaPrompt(ctx context.Context, personas []string, name string, args map[string]string) (*mcp.GetPromptResult, bool) {
	for _, persona := range personas {
		bare, ok := strings.CutPrefix(name, persona+"-")
		if !ok {
			continue
		}
		if pr, err := p.promptStore.Get(ctx, bare); err == nil && pr != nil && pr.Enabled &&
			pr.Scope == prompt.ScopePersona && slices.Contains(pr.Personas, persona) {
			return p.renderPrompt(pr, args)
		}
	}
	return nil, false
}

// renderPrompt substitutes the request arguments into a prompt's content.
// Shared by every serving path so personal and global/persona prompts render
// identically.
func (*Platform) renderPrompt(pr *prompt.Prompt, args map[string]string) (*mcp.GetPromptResult, bool) {
	return buildPromptResult(substituteArgs(pr.Content, args)), true
}

// RegisterRuntimePrompt registers a prompt with the live MCP server at runtime.
// Called after create/update operations on the prompt store.
func (p *Platform) RegisterRuntimePrompt(pr *prompt.Prompt) {
	p.registerDatabasePrompt(pr)
}

// UnregisterRuntimePrompt removes a prompt from the live MCP server at runtime.
// Called after delete operations on the prompt store.
func (p *Platform) UnregisterRuntimePrompt(name string) {
	p.mcpServer.RemovePrompts(name)
	p.promptInfosMu.Lock()
	for i, info := range p.promptInfos {
		if info.Name == name {
			p.promptInfos = append(p.promptInfos[:i], p.promptInfos[i+1:]...)
			break
		}
	}
	p.promptInfosMu.Unlock()
}

// buildPromptResult creates a GetPromptResult with the given content.
func buildPromptResult(content string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Messages: []*mcp.PromptMessage{
			{
				Role: promptRoleUser,
				Content: &mcp.TextContent{
					Text: content,
				},
			},
		},
	}
}
