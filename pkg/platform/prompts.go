// Package platform provides the main platform orchestration.
package platform

import (
	"context"
	"log/slog"
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
		result = strings.ReplaceAll(result, "{"+name+"}", args[name])
	}
	return result
}

// workflowPrompt defines a platform-level workflow prompt with its required toolkits.
type workflowPrompt struct {
	config        PromptConfig
	requiredKinds []string
}

// workflowPrompts returns the set of platform-level workflow prompts.
func workflowPrompts() []workflowPrompt {
	return []workflowPrompt{
		{
			config: PromptConfig{
				Name:        "explore-available-data",
				Description: "Discover what data is available about a topic",
				Content: `Explore what data is available about {topic}.

1. Search the data catalog for datasets related to this topic
2. Present relevant datasets with descriptions, ownership, and quality scores
3. Highlight data products that group related datasets
4. Note any data quality concerns or deprecation warnings`,
				Arguments: []PromptArgumentConfig{
					{Name: "topic", Description: "What topic or subject area?", Required: true},
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
					{Name: "topic", Description: "What should the dashboard visualize?", Required: true},
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
					{Name: "topic", Description: "What should the report cover?", Required: true},
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

// registerDatabasePrompt registers a single database prompt with the MCP server.
func (p *Platform) registerDatabasePrompt(pr *prompt.Prompt) {
	promptContent := pr.Content

	mcpArgs := make([]*mcp.PromptArgument, 0, len(pr.Arguments))
	for _, arg := range pr.Arguments {
		mcpArgs = append(mcpArgs, &mcp.PromptArgument{
			Name:        arg.Name,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}

	p.mcpServer.AddPrompt(&mcp.Prompt{
		Name:        pr.Name,
		Description: pr.Description,
		Arguments:   mcpArgs,
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		resolved := substituteArgs(promptContent, req.Params.Arguments)
		return buildPromptResult(resolved), nil
	})

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
				Role: "user",
				Content: &mcp.TextContent{
					Text: content,
				},
			},
		},
	}
}
