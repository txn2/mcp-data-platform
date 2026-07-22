package promptlayer

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
	// promptArgTopic is the argument name shared across workflow prompts
	// that take a free-form subject ("explore", "create-dashboard", etc.).
	promptArgTopic = "topic"
	// promptRoleUser is the MCP message role for user-authored content.
	// Untyped so it converts to mcp.Role at the call site.
	promptRoleUser = "user"
)

// PromptArgSpec is one argument of an operator- or workflow-defined prompt in
// the caller-neutral shape this package registers from. The caller translates
// its own config type into this.
type PromptArgSpec struct {
	Name        string
	Description string
	Required    bool
}

// PromptSpec is an operator- or workflow-defined prompt in the caller-neutral
// shape this package registers from, decoupling the layer from the caller's
// config types and defaulting rules.
type PromptSpec struct {
	Name        string
	DisplayName string
	Description string
	Content     string
	Arguments   []PromptArgSpec
}

// RegisterPlatformPrompts registers platform-level prompts with the given MCP
// server. It first registers the auto-generated platform overview prompt (if
// applicable), then operator-configured prompts, then workflow prompts, then
// database-stored prompts. No-op on a nil Handle.
func (h *Handle) RegisterPlatformPrompts(server *mcp.Server) {
	if h == nil {
		return
	}
	h.registerAutoPrompt(server)
	for _, spec := range h.operatorPrompts {
		h.registerPromptWithCategory(server, spec, "custom")
	}
	h.registerWorkflowPrompts(server)
	// Mirror the static prompts registered above into the store (as read-only
	// system rows) so they are embedded and searchable (#593). Must run before
	// registerDatabasePrompts so database prompts are not added to promptInfos
	// and re-ingested as system rows.
	h.ingestStaticPrompts(context.Background())
	h.registerDatabasePrompts()
}

// registerAutoPrompt registers the auto-generated "platform-overview" prompt when
// the server description is non-empty. It is skipped if an operator-configured
// prompt already uses the name "platform-overview".
//
// The content is built dynamically based on enabled toolkits, listing what the
// user can do with this platform.
func (h *Handle) registerAutoPrompt(server *mcp.Server) {
	if h.serverDescription == "" {
		return
	}

	// Skip if operator has already defined a prompt with this name.
	if h.isOperatorPrompt(autoPromptName) {
		return
	}

	content := h.buildDynamicOverviewContent()

	server.AddPrompt(&mcp.Prompt{
		Name:        autoPromptName,
		Title:       h.serverName,
		Description: "Overview of this data platform — what it covers and how to use it",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return buildPromptResult(content), nil
	})

	// platform-overview is auto-invoked; it is not included in promptInfos
	// because copy-to-clipboard makes no sense for it.
}

// buildDynamicOverviewContent builds the platform overview content dynamically
// based on the server description and enabled toolkits.
func (h *Handle) buildDynamicOverviewContent() string {
	var b strings.Builder
	b.WriteString(h.serverDescription)
	b.WriteString("\n\n")

	capabilities := h.collectCapabilityBullets()
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
func (h *Handle) collectCapabilityBullets() []string {
	has := map[string]bool{
		kindDataHub:   len(h.registry.GetByKind(kindDataHub)) > 0,
		kindTrino:     len(h.registry.GetByKind(kindTrino)) > 0,
		kindS3:        len(h.registry.GetByKind(kindS3)) > 0,
		kindPortal:    len(h.registry.GetByKind(kindPortal)) > 0,
		kindKnowledge: len(h.registry.GetByKind(kindKnowledge)) > 0,
		kindMemory:    len(h.registry.GetByKind(kindMemory)) > 0,
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
func (h *Handle) registerPromptWithCategory(server *mcp.Server, spec PromptSpec, category string) {
	promptContent := spec.Content

	// Build MCP prompt arguments
	mcpArgs := make([]*mcp.PromptArgument, 0, len(spec.Arguments))
	for _, arg := range spec.Arguments {
		mcpArgs = append(mcpArgs, &mcp.PromptArgument{
			Name:        arg.Name,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}

	server.AddPrompt(&mcp.Prompt{
		Name:        spec.Name,
		Title:       displayOrName(spec.DisplayName, spec.Name),
		Description: spec.Description,
		Arguments:   mcpArgs,
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		resolved := substituteArgs(promptContent, req.Params.Arguments)
		return buildPromptResult(resolved), nil
	})

	// Collect metadata
	info := registry.PromptInfo{
		Name:        spec.Name,
		DisplayName: spec.DisplayName,
		Description: spec.Description,
		Category:    category,
		Content:     spec.Content,
	}
	for _, arg := range spec.Arguments {
		info.Arguments = append(info.Arguments, registry.PromptArgumentInfo{
			Name:        arg.Name,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}
	h.promptInfosMu.Lock()
	h.promptInfos = append(h.promptInfos, info)
	h.promptInfosMu.Unlock()
}

// substituteArgs replaces both {name} and {{name}} placeholders with values
// from args. Double-brace is processed first so that a {{name}} placeholder
// is not accidentally consumed by a {name} replacement of a substring. Keys are
// sorted so output is deterministic when values contain other placeholders.
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
	spec          PromptSpec
	requiredKinds []string
}

// promptExploreAvailableData is the canonical name of the data-exploration
// workflow prompt.
const promptExploreAvailableData = "explore-available-data"

// workflowPrompts returns the set of platform-level workflow prompts.
func workflowPrompts() []workflowPrompt {
	return []workflowPrompt{
		{
			spec: PromptSpec{
				Name:        promptExploreAvailableData,
				DisplayName: "Explore Available Data",
				Description: "Discover what data is available about a topic",
				Content: `Explore what data is available about {topic}.

1. Search the data catalog for datasets related to this topic
2. Present relevant datasets with descriptions, ownership, and quality scores
3. Highlight data products that group related datasets
4. Note any data quality concerns or deprecation warnings`,
				Arguments: []PromptArgSpec{
					{Name: promptArgTopic, Description: "What topic or subject area?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub},
		},
		{
			spec: PromptSpec{
				Name:        "create-interactive-dashboard",
				DisplayName: "Create an Interactive Dashboard",
				Description: "Discover data, build a visualization, and save it as a shareable asset",
				Content: `Create an interactive dashboard about {topic}.

1. Explore what data is available about this topic
2. Query the most relevant datasets
3. Build an interactive visualization with key metrics and trends
4. Save it as an artifact I can view and share`,
				Arguments: []PromptArgSpec{
					{Name: promptArgTopic, Description: "What should the dashboard visualize?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub, kindTrino, kindPortal},
		},
		{
			spec: PromptSpec{
				Name:        "create-a-report",
				DisplayName: "Create a Report",
				Description: "Analyze data and produce a structured Markdown report",
				Content: `Generate a comprehensive report about {topic}.

1. Discover relevant datasets in the data catalog
2. Query and analyze the data for key findings
3. Produce a well-structured Markdown report with tables, metrics, and insights
4. Summarize the key takeaways`,
				Arguments: []PromptArgSpec{
					{Name: promptArgTopic, Description: "What should the report cover?", Required: true},
				},
			},
			requiredKinds: []string{kindDataHub, kindTrino},
		},
		{
			spec: PromptSpec{
				Name:        "trace-data-lineage",
				DisplayName: "Trace Data Lineage",
				Description: "Trace where data comes from and what depends on it",
				Content: `Trace the data lineage for {dataset}.

1. Identify the upstream sources that feed this data
2. Map the downstream consumers that depend on it
3. Show column-level lineage where available
4. Highlight any transformation steps in the pipeline`,
				Arguments: []PromptArgSpec{
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
// has been explicitly disabled via the built-in-prompt disable map.
func (h *Handle) registerWorkflowPrompts(server *mcp.Server) {
	for _, wp := range workflowPrompts() {
		// Skip if explicitly disabled in config
		if h.isBuiltinDisabled(wp.spec.Name) {
			continue
		}

		// Skip if operator already defined this prompt
		if h.isOperatorPrompt(wp.spec.Name) {
			continue
		}

		// Check all required toolkit kinds are present
		if !h.hasAllToolkitKinds(wp.requiredKinds) {
			continue
		}

		h.registerPromptWithCategory(server, wp.spec, "workflow")
	}
}

// isBuiltinDisabled checks if a built-in prompt has been explicitly disabled.
// If the map is nil or the key is absent, the prompt is enabled by default.
func (h *Handle) isBuiltinDisabled(name string) bool {
	if h.builtinPrompts == nil {
		return false
	}
	enabled, exists := h.builtinPrompts[name]
	return exists && !enabled
}

// isOperatorPrompt checks if a prompt name is already defined in operator config.
func (h *Handle) isOperatorPrompt(name string) bool {
	for _, spec := range h.operatorPrompts {
		if spec.Name == name {
			return true
		}
	}
	return false
}

// hasAllToolkitKinds checks that every kind in the list has at least one registered toolkit.
func (h *Handle) hasAllToolkitKinds(kinds []string) bool {
	for _, kind := range kinds {
		if len(h.registry.GetByKind(kind)) == 0 {
			return false
		}
	}
	return true
}

// collectToolkitPromptInfos gathers prompt metadata from toolkits that implement PromptDescriber.
func (h *Handle) collectToolkitPromptInfos() []registry.PromptInfo {
	var infos []registry.PromptInfo
	for _, tk := range h.registry.All() {
		if pd, ok := tk.(registry.PromptDescriber); ok {
			infos = append(infos, pd.PromptInfos()...)
		}
	}
	return infos
}

// AllPromptInfos returns all prompt metadata (platform + toolkit), or nil on a
// nil Handle. Read by the admin and portal prompt REST handlers to surface
// system prompts.
func (h *Handle) AllPromptInfos() []registry.PromptInfo {
	if h == nil {
		return nil
	}
	tkInfos := h.collectToolkitPromptInfos()
	h.promptInfosMu.RLock()
	all := make([]registry.PromptInfo, 0, len(h.promptInfos)+len(tkInfos))
	all = append(all, h.promptInfos...)
	h.promptInfosMu.RUnlock()
	all = append(all, tkInfos...)
	return all
}

// registerDatabasePrompts loads enabled prompts from the database and registers
// their metadata. Called during startup after the prompt store is initialized.
func (h *Handle) registerDatabasePrompts() {
	if h.store == nil {
		return
	}

	enabled := true
	prompts, err := h.store.List(context.Background(), prompt.ListFilter{Enabled: &enabled})
	if err != nil {
		slog.Warn("failed to load prompts from database", logKeyError, err)
		return
	}

	registered := 0
	for i := range prompts {
		// System rows are the ingested static prompts (#593): they are already
		// served via AddPrompt and exist in the store only for indexing/search,
		// so they must not be re-registered as database prompts.
		if prompts[i].Source == prompt.SourceSystem {
			continue
		}
		h.registerDatabasePrompt(&prompts[i])
		registered++
	}
	if registered > 0 {
		slog.Info("loaded prompts from database", "count", registered)
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
//
// Personal prompts are intentionally excluded from this name-keyed metadata
// list: their names are unique only per owner, so tracking them here would
// create colliding entries and expose one user's personal prompts in
// platform-wide metadata (platform_info). They are still served per-caller from
// the store. Only globally-unique scopes (global, persona) are tracked.
func (h *Handle) registerDatabasePrompt(pr *prompt.Prompt) {
	if pr.Scope == prompt.ScopePersonal {
		return
	}
	info := registry.PromptInfo{
		Name:        pr.Name,
		DisplayName: pr.DisplayName,
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
	h.promptInfosMu.Lock()
	h.promptInfos = append(h.promptInfos, info)
	h.promptInfosMu.Unlock()
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
	promptPrefixShared   = "shared-"
)

// displayOrName returns the human display name, falling back to the machine
// name, for the MCP Title field.
func displayOrName(display, name string) string {
	if display != "" {
		return display
	}
	return name
}

// promptDescriptor builds an MCP prompt descriptor under a presented (prefixed)
// name from a stored prompt. Title carries the human display name so clients
// can show "Daily Sales Report" while invoking by machine name.
func promptDescriptor(presentedName string, pr *prompt.Prompt) *mcp.Prompt {
	return &mcp.Prompt{
		Name:        presentedName,
		Title:       displayOrName(pr.DisplayName, pr.Name),
		Description: pr.Description,
		Arguments:   toMCPPromptArgs(pr.Arguments),
	}
}

// ListVisible returns the caller's visible database prompts as MCP descriptors
// with their scope prefix: global-<name> for globals, <persona>-<name> for each
// persona the caller belongs to, and personal-<name> for the caller's own. A
// persona prompt shared with several personas appears once per persona the
// caller is in. Wired as the prompts/list visibility callback.
func (h *Handle) ListVisible(ctx context.Context, email string, personas []string) []*mcp.Prompt {
	if h == nil || h.store == nil {
		return nil
	}
	out := h.listScopedDescriptors(ctx, prompt.ListFilter{Scope: prompt.ScopeGlobal}, promptPrefixGlobal)
	out = append(out, h.listPersonaDescriptors(ctx, personas)...)
	if email != "" {
		out = append(out, h.listScopedDescriptors(ctx, prompt.ListFilter{Scope: prompt.ScopePersonal, OwnerEmail: email}, promptPrefixPersonal)...)
		out = append(out, h.listSharedDescriptors(ctx, email)...)
	}
	return out
}

// listSharedDescriptors lists prompts shared directly with the caller (by
// another user), presenting each as shared-<name>. Shares are looked up via the
// portal share store and the prompt bodies fetched from the prompt store. If two
// shared prompts collide on bare name, the first (most recent share) wins so the
// list and GetByName agree.
func (h *Handle) listSharedDescriptors(ctx context.Context, email string) []*mcp.Prompt {
	if h.shareStore == nil {
		return nil
	}
	refs, err := h.shareStore.ListSharedPromptsWithUser(ctx, "", email)
	if err != nil {
		slog.Warn("failed to list shared prompts", logKeyError, err)
		return nil
	}
	var out []*mcp.Prompt
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		pr, err := h.store.GetByID(ctx, ref.PromptID)
		// Only personal prompts are served via the shared- alias. A prompt that
		// was promoted to a shared scope after being shared is already served
		// under its global-/persona- prefix; serving it again as shared- would
		// duplicate it.
		if err != nil || pr == nil || !pr.Enabled || pr.Scope != prompt.ScopePersonal || seen[pr.Name] {
			continue
		}
		seen[pr.Name] = true
		out = append(out, promptDescriptor(promptPrefixShared+pr.Name, pr))
	}
	return out
}

// listScopedDescriptors lists enabled prompts matching the filter and presents
// each under a fixed scope prefix (for global and personal scopes).
func (h *Handle) listScopedDescriptors(ctx context.Context, filter prompt.ListFilter, prefix string) []*mcp.Prompt {
	enabled := true
	filter.Enabled = &enabled
	prompts, err := h.store.List(ctx, filter)
	if err != nil {
		slog.Warn("failed to list prompts", logKeyError, err, "scope", filter.Scope)
		return nil
	}
	out := make([]*mcp.Prompt, 0, len(prompts))
	for i := range prompts {
		// System rows (ingested static prompts, #593) are already served under
		// their bare name via AddPrompt; skip them here so prompts/list does not
		// show a duplicate global- entry.
		if prompts[i].Source == prompt.SourceSystem {
			continue
		}
		out = append(out, promptDescriptor(prefix+prompts[i].Name, &prompts[i]))
	}
	return out
}

// listPersonaDescriptors lists the caller's persona prompts, presenting each
// once per persona the caller belongs to (the prefix is the persona name).
func (h *Handle) listPersonaDescriptors(ctx context.Context, personas []string) []*mcp.Prompt {
	if len(personas) == 0 {
		return nil
	}
	enabled := true
	personaPrompts, err := h.store.List(ctx, prompt.ListFilter{
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

// GetByName resolves a prefixed prompt name to the caller's visible database
// prompt and renders it for prompts/get. It strips the scope prefix to the bare
// stored name: personal-/global- are reserved tokens; a persona prefix must be
// one of the caller's personas, and the target prompt must actually be shared
// with that persona. Returns (nil, false) when no such visible prompt exists.
// Wired as the prompts/get visibility callback.
//
// The reserved-prefix branches fall through to persona resolution on a miss:
// persona names are operator-defined and may literally be "personal" or
// "global", so a name like "global-report" must still resolve a persona prompt
// when no global prompt by that bare name exists.
func (h *Handle) GetByName(ctx context.Context, email string, personas []string, name string, args map[string]string) (*mcp.GetPromptResult, bool) {
	if h == nil || h.store == nil {
		return nil, false
	}
	pr := h.resolveByName(ctx, email, personas, name)
	if pr == nil {
		return nil, false
	}
	h.auditPromptServe(ctx, pr, serveSurfacePromptsGet, email)
	res := renderPrompt(pr, args)
	attachProvenanceMeta(res, pr)
	return res, true
}

// resolveByName resolves a prefixed prompt name to the caller's visible
// database prompt, or nil. See GetByName for the prefix grammar and the
// reserved-prefix fall-through rationale.
func (h *Handle) resolveByName(ctx context.Context, email string, personas []string, name string) *prompt.Prompt {
	if bare, ok := strings.CutPrefix(name, promptPrefixPersonal); ok {
		if pr := h.ownedPersonalPrompt(ctx, email, bare); pr != nil {
			return pr
		}
	}
	if bare, ok := strings.CutPrefix(name, promptPrefixGlobal); ok {
		if pr := h.globalPrompt(ctx, bare); pr != nil {
			return pr
		}
	}
	if bare, ok := strings.CutPrefix(name, promptPrefixShared); ok {
		// Shared directly with the caller, matched by bare name; the first
		// matching active share wins (consistent with listSharedDescriptors).
		if pr := h.sharedPromptByName(ctx, email, bare); pr != nil {
			return pr
		}
	}
	return h.personaPrompt(ctx, personas, name)
}

// sharedPromptByName finds the prompt shared directly with the caller matching
// the bare name, or nil. Only personal prompts are served via shares; the first
// matching active share wins.
func (h *Handle) sharedPromptByName(ctx context.Context, email, bare string) *prompt.Prompt {
	if email == "" || h.shareStore == nil {
		return nil
	}
	refs, err := h.shareStore.ListSharedPromptsWithUser(ctx, "", email)
	if err != nil {
		return nil
	}
	for _, ref := range refs {
		pr, err := h.store.GetByID(ctx, ref.PromptID)
		if err != nil || pr == nil || !pr.Enabled || pr.Scope != prompt.ScopePersonal {
			continue
		}
		if pr.Name == bare {
			return pr
		}
	}
	return nil
}

// ownedPersonalPrompt resolves the caller's own personal prompt of the bare name.
func (h *Handle) ownedPersonalPrompt(ctx context.Context, email, bare string) *prompt.Prompt {
	if email == "" {
		return nil
	}
	pr, err := h.store.GetPersonal(ctx, email, bare)
	if err != nil || pr == nil || !pr.Enabled {
		return nil
	}
	return pr
}

// globalPrompt resolves the global prompt of the bare name.
func (h *Handle) globalPrompt(ctx context.Context, bare string) *prompt.Prompt {
	pr, err := h.store.Get(ctx, bare)
	// System rows are ingested static prompts already served under their bare
	// name via AddPrompt; do not also serve them under the global- prefix.
	if err != nil || pr == nil || !pr.Enabled || pr.Scope != prompt.ScopeGlobal || pr.Source == prompt.SourceSystem {
		return nil
	}
	return pr
}

// personaPrompt resolves a <persona>-<name> prompt for a caller who belongs
// to that persona and is shared the target prompt. Because both persona names
// and prompt names may contain hyphens, a presented name can split more than one
// way (persona "data" + "engineer-x" vs persona "data-engineer" + "x"); personas
// are tried longest-name-first so the most specific persona prefix wins
// deterministically.
func (h *Handle) personaPrompt(ctx context.Context, personas []string, name string) *prompt.Prompt {
	ordered := append([]string(nil), personas...)
	slices.SortStableFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	for _, persona := range ordered {
		bare, ok := strings.CutPrefix(name, persona+"-")
		if !ok {
			continue
		}
		if pr, err := h.store.Get(ctx, bare); err == nil && pr != nil && pr.Enabled &&
			pr.Scope == prompt.ScopePersona && slices.Contains(pr.Personas, persona) {
			return pr
		}
	}
	return nil
}

// renderPrompt substitutes the request arguments into a prompt's content.
// Shared by every serving path so personal and global/persona prompts render
// identically.
func renderPrompt(pr *prompt.Prompt, args map[string]string) *mcp.GetPromptResult {
	return buildPromptResult(substituteArgs(pr.Content, args))
}

// RegisterRuntimePrompt records a prompt's metadata at runtime. Called after
// create/update operations on the prompt store. No-op on a nil Handle.
func (h *Handle) RegisterRuntimePrompt(pr *prompt.Prompt) {
	if h == nil {
		return
	}
	h.registerDatabasePrompt(pr)
}

// UnregisterRuntimePrompt removes a database prompt's tracked metadata. It does
// NOT remove the prompt from the static MCP registry: database prompts are never
// placed there (see registerDatabasePrompt), so removing by bare name could only
// ever delete an unrelated built-in/operator/toolkit prompt of the same name.
// Only the name-keyed metadata entry (global/persona scope) is dropped. No-op
// on a nil Handle.
func (h *Handle) UnregisterRuntimePrompt(name string) {
	if h == nil {
		return
	}
	h.promptInfosMu.Lock()
	for i, info := range h.promptInfos {
		if info.Name == name {
			h.promptInfos = append(h.promptInfos[:i], h.promptInfos[i+1:]...)
			break
		}
	}
	h.promptInfosMu.Unlock()
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
