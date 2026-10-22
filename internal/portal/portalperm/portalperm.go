// Package portalperm decides which portal area a request belongs to and
// whether a persona's portal permissions admit it (#2225).
//
// A persona's Portal block (persona.PortalAccess) sets one level per area. The
// navigation hides what a persona lacks, and this is the half that makes the
// setting hold for a client that never reads the navigation: every route of an
// area answers 403 when the level does not allow the request. The route table
// is here, apart from the gate that applies it, so the table can be read and
// tested as one list.
package portalperm

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// The areas a refusal names, in the words the persona editor uses.
const (
	AreaAssets      = "assets"
	AreaKnowledge   = "knowledge"
	AreaCatalog     = "catalog"
	AreaAPIs        = "apis"
	AreaFeedback    = "feedback"
	AreaPrompts     = "prompts"
	AreaAutomations = "automations"
	AreaSearch      = "search"
	AreaMemory      = "memory"
	AreaDownload    = "download"
	// AreaExternal names what an external persona's people never reach.
	AreaExternal = "external"
)

const (
	portalPrefix = "/api/v1/portal/"
	apisPrefix   = "/api/v1/apis"
)

// externalWithheld are the routes a person of an external persona never
// reaches (#2092): the people directory and mention candidates, API keys, and
// the channels a share is posted to.
var externalWithheld = map[string]bool{
	"users": true, "mention-candidates": true, "api-keys": true, "channels": true,
}

// segmentAreas maps the first path segment after /api/v1/portal/ to the
// switch that governs it. A segment absent here and from the leveled areas
// below belongs to no area: the caller's own profile, keys, sessions and
// notification settings, which every persona keeps.
var segmentAreas = map[string]struct {
	area string
	on   func(persona.PortalAccess) *bool
}{
	"threads":            {AreaFeedback, func(a persona.PortalAccess) *bool { return a.Feedback }},
	"worklist":           {AreaFeedback, func(a persona.PortalAccess) *bool { return a.Feedback }},
	"feedback":           {AreaFeedback, func(a persona.PortalAccess) *bool { return a.Feedback }},
	"prompts":            {AreaPrompts, func(a persona.PortalAccess) *bool { return a.Prompts }},
	"prompt-collections": {AreaPrompts, func(a persona.PortalAccess) *bool { return a.Prompts }},
	"shared-prompts":     {AreaPrompts, func(a persona.PortalAccess) *bool { return a.Prompts }},
	"scripts":            {AreaAutomations, func(a persona.PortalAccess) *bool { return a.Automations }},
	"search":             {AreaSearch, func(a persona.PortalAccess) *bool { return a.Search }},
	"memory":             {AreaMemory, func(a persona.PortalAccess) *bool { return a.Memory }},
	"knowledge":          {AreaMemory, func(a persona.PortalAccess) *bool { return a.Memory }},
}

// Refused returns the area a request belongs to when the permissions do not
// admit it, and "" when they do or the request belongs to no area.
func Refused(a persona.PortalAccess, method, path string) string {
	if strings.HasPrefix(path, apisPrefix) {
		return refuseBelow(a.APIs, persona.PortalBrowse, AreaAPIs)
	}
	rest, ok := strings.CutPrefix(path, portalPrefix)
	if !ok {
		return ""
	}
	segments := strings.Split(rest, "/")
	write := isWrite(method)
	if a.External && externalWithheld[segments[0]] {
		return AreaExternal
	}
	switch segments[0] {
	case "assets", "collections":
		return refusedItem(a, method, segments)
	case "knowledge-pages":
		return refuseBelow(a.Knowledge, readOrWrite(write), AreaKnowledge)
	case "catalog":
		return refuseBelow(a.Catalog, readOrWrite(write), AreaCatalog)
	}
	if s, ok := segmentAreas[segments[0]]; ok && !persona.On(s.on(a)) {
		return s.area
	}
	return ""
}

// refusedItem is the assets area: creating an asset or collection, or copying
// one, needs the own level; a PDF needs download; sign-off is feedback. Every
// other route of an item is decided by the item's own sharing, which a person
// at the shared level reads through.
func refusedItem(a persona.PortalAccess, method string, segments []string) string {
	last := segments[len(segments)-1]
	creates := method == http.MethodPost && (len(segments) == 1 || last == "copy")
	switch {
	case creates:
		return refuseBelow(a.Assets, persona.PortalOwn, AreaAssets)
	case last == "pdf" && !persona.On(a.Download):
		return AreaDownload
	case last == "signoff" && !persona.On(a.Feedback):
		return AreaFeedback
	}
	return ""
}

func refuseBelow(level, want, area string) string {
	if persona.AtLeast(level, want) {
		return ""
	}
	return area
}

func readOrWrite(write bool) string {
	if write {
		return persona.PortalWrite
	}
	return persona.PortalRead
}

func isWrite(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}
