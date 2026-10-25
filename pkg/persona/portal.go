package persona

import (
	"fmt"
	"slices"
)

// Portal area levels (#2225). A persona governs what an agent may call through
// Tools, Connections and APIRoutes; its Portal block governs what its people
// may do in the portal, one setting per area, enforced on every route of the
// area and reported on /api/v1/portal/me so the navigation shows only what the
// persona has.
const (
	// PortalNone takes an area away: its navigation entry and every route.
	PortalNone = "none"
	// PortalShared reads what was shared with the person: for assets and
	// collections the items shared with them, for resources their own folder,
	// their persona's folder and Global. Nothing is created.
	PortalShared = "shared"
	// PortalOwn creates and owns items: assets and collections, or resources
	// uploaded to the person's own folder, which administrators see.
	PortalOwn = "own"
	// PortalPersonaFolders is resources as before #2225: the person's own
	// folder, their persona's folder and Global.
	PortalPersonaFolders = "persona"
	// PortalRead reads an area: knowledge pages, the catalog.
	PortalRead = "read"
	// PortalWrite reads and writes an area.
	PortalWrite = "write"
	// PortalBrowse opens the portal's API browser, still filtered by the
	// persona's connections and API routes.
	PortalBrowse = "browse"
	// PortalList lists the scratch tables registered on the connections the
	// persona reaches (#2257).
	PortalList = "list"
	// PortalRegister lists them and registers a table over a file.
	PortalRegister = "register"
	// PortalView lists and opens every external person's personal folder and
	// its files (#2290).
	PortalView = "view"
	// PortalManage also renames, versions, moves, deletes and shares them.
	PortalManage = "manage"
)

// The tools a persona's portal defaults are read from: a persona with no
// Portal block keeps what its tools let it reach in the portal before #2225.
const (
	portalToolApplyKnowledge = "apply_knowledge"
	portalToolFetch          = "fetch"
)

// The levels each area admits, lowest first.
var (
	assetLevels     = []string{PortalShared, PortalOwn}
	resourceLevels  = []string{PortalNone, PortalShared, PortalOwn, PortalPersonaFolders}
	knowledgeLevels = []string{PortalNone, PortalRead, PortalWrite}
	catalogLevels   = []string{PortalNone, PortalRead, PortalWrite}
	apiLevels       = []string{PortalNone, PortalBrowse}
	scratchLevels   = []string{PortalNone, PortalList, PortalRegister}
	externalLevels  = []string{PortalNone, PortalView, PortalManage}
)

// PortalAccess is a persona's portal permissions (#2225). An empty level or a
// nil switch is unset, and resolves to what the persona reached before the
// setting existed (PortalPermissions); the persona editor writes every field.
type PortalAccess struct {
	// Assets is assets and collections: shared or own.
	Assets string `json:"assets,omitempty" yaml:"assets,omitempty"`
	// Resources is managed resources: none, shared, own or persona.
	Resources string `json:"resources,omitempty" yaml:"resources,omitempty"`
	// Knowledge is knowledge pages: none, read or write.
	Knowledge string `json:"knowledge,omitempty" yaml:"knowledge,omitempty"`
	// Catalog is the platform catalog: none, read or write.
	Catalog string `json:"catalog,omitempty" yaml:"catalog,omitempty"`
	// APIs is the portal API browser: none or browse.
	APIs string `json:"apis,omitempty" yaml:"apis,omitempty"`
	// ScratchTables is the scratch-table routes and page: none, list or
	// register (#2257). The connections a table sits on are still the
	// persona's Connections.
	ScratchTables string `json:"scratch_tables,omitempty" yaml:"scratch_tables,omitempty"`
	// Feedback is threads, comments, activity, worklists and sign-off.
	Feedback *bool `json:"feedback,omitempty" yaml:"feedback,omitempty"`
	// Prompts is the prompt library.
	Prompts *bool `json:"prompts,omitempty" yaml:"prompts,omitempty"`
	// Automations is managed scripts in the portal; the script tools an agent
	// calls are governed by Tools. It replaces hide_automations (#2109).
	Automations *bool `json:"automations,omitempty" yaml:"automations,omitempty"`
	// Search is the portal's unified search.
	Search *bool `json:"search,omitempty" yaml:"search,omitempty"`
	// Memory is memory and insights.
	Memory *bool `json:"memory,omitempty" yaml:"memory,omitempty"`
	// Download is exporting an asset or a resource as PDF; the portal names
	// it PDF export (#2257). Reading content is the item's own sharing.
	Download *bool `json:"download,omitempty" yaml:"download,omitempty"`
	// Uploads lets the people of an external persona upload files to their
	// own folder (#2258, #2290), which staff reach through ExternalFiles. Unset
	// is off; an internal persona's people upload by their Resources level
	// and this is dropped from its resolved block.
	Uploads *bool `json:"uploads,omitempty" yaml:"uploads,omitempty"`
	// History lets the persona's people read the earlier versions of an asset
	// shared with them as viewer (#2226, #2290). Unset is off: the owner, an
	// editor and an administrator read them, and a viewer reads the current
	// version only.
	History *bool `json:"history,omitempty" yaml:"history,omitempty"`
	// ExternalFiles is every external person's personal folder: none, view
	// or manage (#2290). Unset is none; an external persona has none.
	ExternalFiles string `json:"external_files,omitempty" yaml:"external_files,omitempty"`
	// External is reported, never set: the persona is external (#2092), so
	// the portal offers its people no people directory and no API keys.
	External bool `json:"external,omitempty" yaml:"-"`
}

// Resolve is the block with every area resolved as PortalPermissions
// resolves a persona's: an area left unset reads from granted.
func (a PortalAccess) Resolve(granted []string) PortalAccess {
	return (&Persona{Portal: &a}).PortalPermissions(granted)
}

// PortalPermissions is a persona's portal access with every field set. A
// field the persona leaves unset resolves to what the persona reached before
// #2225: assets own, resources persona, the API browser and every switch on;
// knowledge write when granted holds apply_knowledge and read otherwise; the
// catalog write when it holds apply_knowledge and fetch, read with fetch
// alone, none without it. granted is the persona's tools as the portal has
// always read them: its rules over the tools the deployment registers, so a
// tool the deployment does not register grants nothing here either.
func (p *Persona) PortalPermissions(granted []string) PortalAccess {
	var set PortalAccess
	if p != nil && p.Portal != nil {
		set = *p.Portal
	}
	tool := func(name string) bool { return slices.Contains(granted, name) }
	out := PortalAccess{
		Assets:    levelOr(set.Assets, PortalOwn),
		Resources: levelOr(set.Resources, PortalPersonaFolders),
		Knowledge: levelOr(set.Knowledge, defaultKnowledge(tool)),
		Catalog:   levelOr(set.Catalog, defaultCatalog(tool)),
		APIs:      levelOr(set.APIs, PortalBrowse),
		// Scratch tables were every persona's before #2257.
		ScratchTables: levelOr(set.ScratchTables, PortalRegister),
		Feedback:      switchOr(set.Feedback),
		Prompts:       switchOr(set.Prompts),
		Automations:   switchOr(set.Automations),
		Search:        switchOr(set.Search),
		Memory:        switchOr(set.Memory),
		Download:      switchOr(set.Download),
		History:       offOr(set.History),
		ExternalFiles: levelOr(set.ExternalFiles, PortalNone),
	}
	// A block resolved for an external persona carries External, so reading
	// it again keeps the limits.
	if (p != nil && p.External) || set.External {
		out.Uploads = offOr(set.Uploads)
		out = externalLimits(out)
	}
	return out
}

// StoredPortal is the persona's portal block resolved without the limits
// External sets: what the persona editor shows and saves, so marking a
// persona external and back leaves its levels where they were (#2257).
// granted is as PortalPermissions takes it.
func (p *Persona) StoredPortal(granted []string) PortalAccess {
	var set PortalAccess
	if p != nil && p.Portal != nil {
		set = *p.Portal
	}
	set.External = false
	out := (&Persona{Portal: &set}).PortalPermissions(granted)
	out.Uploads = set.Uploads
	return out
}

// externalLimits holds an external persona's portal to what #2092 allows:
// assets shared with its people, resources no more than shared (which for an
// external persona leaves out Global, #2257), no knowledge, catalog, API
// browser, scratch tables, search, prompts, automations, memory or other
// external people's files (#2290). Feedback, PDF export, uploads and earlier
// versions stay as the persona sets them.
func externalLimits(a PortalAccess) PortalAccess {
	off := func() *bool { v := false; return &v }
	a.Assets = PortalShared
	if a.Resources != PortalNone {
		a.Resources = PortalShared
	}
	a.Knowledge, a.Catalog, a.APIs, a.ScratchTables, a.ExternalFiles = PortalNone, PortalNone, PortalNone, PortalNone, PortalNone
	a.Search, a.Prompts, a.Automations, a.Memory = off(), off(), off(), off()
	a.External = true
	return a
}

// FullPortalAccess is every area at its highest level: what an administrator
// has, whatever their persona says.
func FullPortalAccess() PortalAccess {
	on := func() *bool { v := true; return &v }
	return PortalAccess{
		Assets: PortalOwn, Resources: PortalPersonaFolders, Knowledge: PortalWrite,
		Catalog: PortalWrite, APIs: PortalBrowse, ScratchTables: PortalRegister,
		Feedback: on(), Prompts: on(), Automations: on(), Search: on(), Memory: on(), Download: on(),
		History: on(), ExternalFiles: PortalManage,
	}
}

// On reports whether a switch is on; an unset switch is on.
func On(v *bool) bool { return v == nil || *v }

// Validate refuses a level an area does not admit. Unset fields are valid.
func (a *PortalAccess) Validate() error {
	if a == nil {
		return nil
	}
	for _, f := range []struct {
		area, level string
		levels      []string
	}{
		{"assets", a.Assets, assetLevels},
		{"resources", a.Resources, resourceLevels},
		{"knowledge", a.Knowledge, knowledgeLevels},
		{"catalog", a.Catalog, catalogLevels},
		{"apis", a.APIs, apiLevels},
		{"scratch_tables", a.ScratchTables, scratchLevels},
		{"external_files", a.ExternalFiles, externalLevels},
	} {
		if f.level != "" && !slices.Contains(f.levels, f.level) {
			return fmt.Errorf("portal.%s: %q is not one of %v", f.area, f.level, f.levels)
		}
	}
	return nil
}

// Differs lists the areas whose resolved level differs between two blocks,
// in the order the editor shows them: what an external persona's limits
// took from the levels it stored (#2257).
func Differs(stored, effective PortalAccess) []string {
	var out []string
	for _, f := range []struct {
		area string
		a, b string
	}{
		{"assets", stored.Assets, effective.Assets},
		{"resources", stored.Resources, effective.Resources},
		{"knowledge", stored.Knowledge, effective.Knowledge},
		{"catalog", stored.Catalog, effective.Catalog},
		{"apis", stored.APIs, effective.APIs},
		{"scratch_tables", stored.ScratchTables, effective.ScratchTables},
		{"feedback", onText(stored.Feedback), onText(effective.Feedback)},
		{"prompts", onText(stored.Prompts), onText(effective.Prompts)},
		{"automations", onText(stored.Automations), onText(effective.Automations)},
		{"search", onText(stored.Search), onText(effective.Search)},
		{"memory", onText(stored.Memory), onText(effective.Memory)},
		{"download", onText(stored.Download), onText(effective.Download)},
		{"external_files", stored.ExternalFiles, effective.ExternalFiles},
	} {
		if f.a != f.b {
			out = append(out, f.area)
		}
	}
	return out
}

func onText(v *bool) string {
	if On(v) {
		return "on"
	}
	return "off"
}

// AtLeast reports whether level reaches want in the order an area's levels
// are listed. Both must be levels of the same area.
func AtLeast(level, want string) bool {
	for _, levels := range [][]string{assetLevels, resourceLevels, knowledgeLevels, catalogLevels, apiLevels, scratchLevels, externalLevels} {
		have, need := slices.Index(levels, level), slices.Index(levels, want)
		if have >= 0 && need >= 0 {
			return have >= need
		}
	}
	return false
}

func defaultKnowledge(tool func(string) bool) string {
	if tool(portalToolApplyKnowledge) {
		return PortalWrite
	}
	return PortalRead
}

func defaultCatalog(tool func(string) bool) string {
	switch {
	case tool(portalToolApplyKnowledge) && tool(portalToolFetch):
		return PortalWrite
	case tool(portalToolFetch):
		return PortalRead
	default:
		return PortalNone
	}
}

func levelOr(level, fallback string) string {
	if level == "" {
		return fallback
	}
	return level
}

// offOr resolves a switch whose unset value is off.
func offOr(v *bool) *bool {
	out := v != nil && *v
	return &out
}

func switchOr(v *bool) *bool {
	out := On(v)
	return &out
}
