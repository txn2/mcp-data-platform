package resource

import (
	"slices"
	"strings"
)

// Claims represents the identity information needed for resource permission checks.
type Claims struct {
	Sub             string   // Keycloak subject (user ID)
	Email           string   // user email
	Personas        []string // persona names the user belongs to
	Roles           []string // raw roles from auth (may have prefix, e.g., "dp_admin")
	IsAdmin         bool     // resolved by the caller from persona config
	AdminOfPersonas []string // persona names this user can admin (resolved by caller from role patterns)
	// OnBehalfOf is the address of the person an unattended caller acts for,
	// carried from PlatformContext.OnBehalfOfEmail. A managed-script run
	// authenticates as script:<name>, a principal that owns nothing a person
	// owns, so claims built on the principal alone refuse a run the very files
	// its own author can edit -- and file a resource it creates in a library
	// nobody can see. Every rule below that turns on "is this you?" reads this
	// too, so a run reaches what its author reaches and nothing else (#1419,
	// #1487).
	//
	// Empty for every human caller, which is what keeps this inert everywhere
	// else. An empty value must never match an empty owner or an empty scope
	// id: absence of an identity is not a shared identity.
	OnBehalfOf string
}

// personaAdminInfix is the role substring that marks a persona-admin grant.
// A role of any prefix carrying it (for example "dp_persona-admin:finance")
// grants admin authority over the named persona's resources.
const personaAdminInfix = "persona-admin:"

// BuildClaims assembles permission claims from an authenticated caller's
// identity. It owns the roles-to-persona-admin mapping so every surface that
// must apply the resource read rule — the resources middleware, prompt
// attachment serving (#1013), the attachment REST handler — derives claims
// identically instead of each reimplementing it.
//
// persona is the caller's single resolved persona; pass "" when none is
// resolved. It is separate from roles because persona membership is resolved
// from roles by the platform's persona registry, which this package does not
// see.
func BuildClaims(sub, email, persona string, roles []string, isAdmin bool) Claims {
	c := Claims{
		Sub:             sub,
		Email:           email,
		Roles:           roles,
		IsAdmin:         isAdmin,
		AdminOfPersonas: PersonaAdminRoles(roles),
	}
	if persona != "" {
		c.Personas = []string{persona}
	}
	return c
}

// ActingFor returns the claims with the address of the person an unattended
// caller acts for. It is a step after BuildClaims rather than another parameter
// on it because only the surfaces an unattended caller reaches have an address
// to supply, and a surface that has none says nothing.
//
// An empty address is a no-op, so a surface can pass whatever its context
// carries without asking whether the caller is one.
func (c Claims) ActingFor(address string) Claims {
	c.OnBehalfOf = address
	return c
}

// PersonaAdminRoles extracts the persona names a role set grants admin
// authority over, tolerating any role prefix.
func PersonaAdminRoles(roles []string) []string {
	var out []string
	for _, r := range roles {
		if _, name, ok := strings.Cut(r, personaAdminInfix); ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// CanWriteScope checks whether the caller has write permission for the given scope.
func CanWriteScope(c Claims, scope Scope, scopeID string) bool {
	switch scope {
	case ScopeUser:
		// Both sides of every identity comparison here must be non-empty. A
		// caller with no subject and a scope with no id are not the same
		// person: absence of an identity is not a shared identity, and
		// ValidateScope refuses an empty user scope id at every door anyway.
		//
		// Any authenticated user can write to their own user scope, and an
		// unattended caller to the scope of the person it acts for -- which is
		// the same scope, reached by the only identifier such a caller has.
		// Platform admins can write to any user scope.
		//
		// The address is tested alongside the subject because a user scope is
		// keyed by either: VisibleScopes has always matched both, so a library
		// named by address was one its owner could see and could not manage --
		// an administrator scoping a resource to somebody by email, and a
		// script filing one into its author's library, both produce a row its
		// owner is refused. Read and write name the same person here.
		return isOwnScope(c, scopeID) || isPlatformAdmin(c)
	case ScopePersona:
		return isPlatformAdmin(c) || isPersonaAdmin(c, scopeID)
	case ScopeGlobal:
		return isPlatformAdmin(c)
	default:
		return false
	}
}

// CanMoveToLibrary reports whether the caller may file a resource into the named
// library. The caller must separately be allowed to modify the resource itself
// (CanModifyResource); this answers only the destination half.
//
// It is deliberately looser than CanWriteScope on one arm and identical on every
// other. Uploading into a persona library takes that persona's admin role, and
// that stays: putting new material in front of a persona's members is the
// persona administrator's call. Moving a file you already own into a persona you
// BELONG to is a different act -- the file exists, you own it, and you are one of
// the people who will read it -- so membership is enough.
//
// Widening CanWriteScope itself would have been the smaller diff and the wrong
// one: it is what the upload route checks and what the Resources page derives its
// Upload control and its scope tabs from, so every member of every persona would
// have gained an upload door nobody asked to open.
//
// Global stays admin-only through CanWriteScope, and so does another person's
// library.
func CanMoveToLibrary(c Claims, scope Scope, scopeID string) bool {
	if CanWriteScope(c, scope, scopeID) {
		return true
	}
	return scope == ScopePersona && scopeID != "" && slices.Contains(c.Personas, scopeID)
}

// CanModifyResource checks whether the caller can update or delete a resource.
// The caller must be the recorded uploader OR have write permission for the
// scope the resource lives in.
//
// "The recorded uploader" is uploadedByCaller, and it is one rule for the person
// and for anything acting for them, which is what keeps the two from diverging
// in either direction (#1419, #1576). See it for what each half is for.
func CanModifyResource(c Claims, r *Resource) bool {
	if uploadedByCaller(c, r) {
		return true
	}
	return CanWriteScope(c, r.Scope, r.ScopeID)
}

// CanAccessResource checks whether the caller may see a specific resource at
// all: it is inside their visible scopes, OR they hold write authority over the
// scope it lives in (a platform admin, or that persona's admin).
//
// The second clause is what separates this from CanReadResource. VisibleScopes
// is membership-based and grants an admin no cross-persona read, so a platform
// admin who uploads a persona-scoped resource — which CanWriteScope explicitly
// permits — was then refused GET, PATCH and DELETE on it: they could create
// material they could neither manage nor remove. Use this as the visibility gate
// on a resource the caller names by id; CanReadResource remains the membership
// rule for enumeration -- a listing, a search -- because that hands the caller
// material they did not name.
//
// It deliberately checks CanWriteScope rather than CanModifyResource: the latter
// also grants the original uploader, and that grant is not re-derived from
// current authority. An admin who uploaded into another user's scope and then
// lost their admin role would otherwise keep reading, editing, and deleting that
// user's private file forever, because the uploader_sub on the row never
// changes. Every legitimate uploader whose authority came from their own scope
// (a user uploading to their own user scope) is already covered by
// CanReadResource.
//
// "Names by id" covers a file named by its mcp:// URI as well, which is how an
// asset reference declares one, and a file named by its mcp:resource: reference,
// which is how knowledge fetch dereferences one (#1584). Each is a question
// about what the caller may reach, asked about a file they stated; asking them
// on the membership rule told one administrator, in one session, both that they
// may read a file and that they may not.
//
// The one narrow exception is an unattended caller acting for the person who
// uploaded the file INTO THEIR OWN LIBRARY (uploadedBy). That is the same grant
// CanReadResource already gives the person, reached by the only identifier such
// a caller has, and it carries none of the decay the warning above is about --
// see uploadedBy for why the scope is part of the test. What a caller may CHANGE
// is a wider set than this, and deliberately so; a surface that authorizes on
// CanModifyResource alone is granting that wider set (#1576).
func CanAccessResource(c Claims, r *Resource) bool {
	return CanReadResource(c, r) || CanWriteScope(c, r.Scope, r.ScopeID) || uploadedBy(c, r)
}

// CanReadResource checks whether the caller can read a specific resource.
func CanReadResource(c Claims, r *Resource) bool {
	for _, sf := range VisibleScopes(c) {
		if sf.Scope == r.Scope {
			if sf.Scope == ScopeGlobal {
				return true
			}
			if sf.ScopeID == r.ScopeID {
				return true
			}
		}
	}
	return false
}

// CanSeeLibrary reports whether the caller may read the named library at all.
//
// It is the listing rule plus write authority, which is the same pair
// CanAccessResource applies to one resource: membership is what makes a library
// visible, and an administrator who may write a library they are not a member
// of must still be able to read and reorganize it.
//
// A platform administrator who uploads into a persona they do not belong to is
// the case this exists for. CanWriteScope permits the upload and
// CanAccessResource permits every later read of the file by id, so a library
// rule built on membership alone left that administrator holding material they
// could create and could not find.
func CanSeeLibrary(c Claims, lib ScopeFilter) bool {
	for _, sf := range VisibleScopes(c) {
		if sf.Scope == lib.Scope && (sf.Scope == ScopeGlobal || sf.ScopeID == lib.ScopeID) {
			return true
		}
	}
	return CanWriteScope(c, lib.Scope, lib.ScopeID)
}

// narrowScopes keeps the visible scopes matching the caller-requested scope and
// optional scope id. A request naming a scope the caller has none of narrows to
// nothing, which is a listing with no rows rather than an unfiltered one.
func narrowScopes(visible []ScopeFilter, scopeParam, scopeIDParam string) []ScopeFilter {
	var narrowed []ScopeFilter
	for _, sf := range visible {
		if string(sf.Scope) == scopeParam {
			if scopeIDParam == "" || sf.ScopeID == scopeIDParam {
				narrowed = append(narrowed, sf)
			}
		}
	}
	return narrowed
}

// ListScopes is the visibility predicate one listing request runs under: the
// scopes it may read, and whether it is unrestricted.
//
// An unnarrowed listing by a platform administrator is every library in the
// deployment. That is what the control asking for it has always been labeled,
// and it is the only reading under which an administrator's own uploads into a
// persona library appear in a listing at all. Every other caller's unnarrowed
// listing is their visible scopes, unchanged.
//
// A narrowed listing accepts one library the caller may see or write, which is
// the rule a folder move already runs under (CanSeeLibrary). Naming a library
// the caller may not reach narrows to nothing rather than widening: the empty
// set is a listing with no rows, which is what the store returns for it.
//
// A scope named without an id is a kind rather than a library -- "persona",
// with no persona -- so it can only narrow the caller's own memberships. The
// portal never sends one; a hand-written request that does gets the membership
// answer rather than a widened one.
func ListScopes(c Claims, scopeParam, scopeIDParam string) (scopes []ScopeFilter, all bool) {
	if scopeParam == "" {
		if isPlatformAdmin(c) {
			return nil, true
		}
		return VisibleScopes(c), false
	}
	named := ScopeFilter{Scope: Scope(scopeParam), ScopeID: scopeIDParam}
	if scopeIDParam != "" && CanSeeLibrary(c, named) {
		return []ScopeFilter{named}, false
	}
	return narrowScopes(VisibleScopes(c), scopeParam, scopeIDParam), false
}

// VisibleScopes returns the set of (scope, scope_id) tuples the caller is
// allowed to see. Always derived from claims, never from request input.
func VisibleScopes(c Claims) []ScopeFilter {
	var filters []ScopeFilter

	// Every authenticated user sees global resources.
	filters = append(filters, ScopeFilter{Scope: ScopeGlobal})

	// User sees their own resources (match by sub or address so admins can
	// scope resources to users by email address). For an unattended caller the
	// address is the person it acts for, which is also where its own writes
	// land; see PersonAddress for why its own Email is not consulted.
	if c.Sub != "" {
		filters = append(filters, ScopeFilter{Scope: ScopeUser, ScopeID: c.Sub})
	}
	if addr := PersonAddress(c); addr != "" && addr != c.Sub {
		filters = append(filters, ScopeFilter{Scope: ScopeUser, ScopeID: addr})
	}

	// User sees resources for each persona they belong to.
	for _, p := range c.Personas {
		filters = append(filters, ScopeFilter{Scope: ScopePersona, ScopeID: p})
	}

	return filters
}

// PersonAddress is the address of the person these claims speak for.
//
// For anyone acting as themselves that is their own. For an unattended caller
// it is the person it acts for, and its own Email is deliberately not consulted:
// a managed-script run carries its OWNER's address for accountability while
// presenting its version AUTHOR's roles, and after a transfer those are
// different people. Treating the owner's address as a scope the run may reach
// would combine one person's authority with another's ownership, which is a
// pairing neither of them has (#1419).
//
// It is exported because it is also what a write records as its author: a row a
// script produced has to name the person whose authority ran, or a scheduled
// refresh reads in the version history as having been made by whoever happens to
// own the script.
func PersonAddress(c Claims) string {
	if c.OnBehalfOf != "" {
		return c.OnBehalfOf
	}
	return c.Email
}

// isOwnScope reports whether a user scope id names the person these claims speak
// for, by either of the two identifiers a user scope is keyed on. Both sides
// must be non-empty: absence of an identity is not a shared identity.
//
// The comparison is exact, deliberately. VisibleScopes emits the address
// verbatim and CanReadResource compares it verbatim, and those are what the
// store's listing predicate is built from; folding case here alone would make a
// resource modifiable and deletable by somebody it never appears in a listing
// for. Read and write have to name the same person, so they compare the same
// way -- and a case-folded scope id is a listing problem to solve in the store,
// not an authority to grant ahead of it.
func isOwnScope(c Claims, scopeID string) bool {
	if scopeID == "" {
		return false
	}
	if scopeID == c.Sub {
		return true
	}
	return scopeID == PersonAddress(c)
}

// uploadedBy reports whether a resource is one the person an unattended caller
// acts for uploaded INTO THEIR OWN LIBRARY, matched on the address the row
// records. It is the visibility arm: CanAccessResource uses this, and
// CanModifyResource uses the wider, symmetric uploadedByCaller.
//
// The scope is part of the test rather than incidental to it. The uploader arm
// is the one grant on a resource that is never re-derived from current
// authority, which is why CanAccessResource refuses it: an administrator who
// uploaded into somebody else's scope and later lost the role would otherwise
// keep reading that person's private file forever. Admitting an unattended
// caller to SEE a resource on the address alone would hand a script exactly that
// permanent reach over material its author was only ever entrusted to place.
//
// Narrowing it to a file sitting in its own uploader's user library leaves only
// the case this exists for: a person's own script reaching a file they uploaded
// through the portal, which files a personal resource under the uploader's
// subject. Everything they uploaded on somebody else's behalf -- another user's
// scope, a persona library, the global one -- is excluded, and reaching those
// still takes the scope authority CanWriteScope asks for.
//
// The address comparison here IS case-folded, unlike isOwnScope above, and the
// difference is not an oversight: this one reads a value off the row rather than
// a scope id the store lists by, so there is no listing predicate for it to
// disagree with. It matches the asset toolkit's ownsResource, which folds for
// the same reason.
//
// Every side must be non-empty: absence of an identity is not a shared identity.
func uploadedBy(c Claims, r *Resource) bool {
	return c.OnBehalfOf != "" && r.UploaderEmail != "" &&
		r.Scope == ScopeUser && r.UploaderSub != "" && r.UploaderSub == r.ScopeID &&
		strings.EqualFold(r.UploaderEmail, c.OnBehalfOf)
}

// uploadedByCaller reports whether a resource records this caller as its
// uploader. It is the uploader arm of CanModifyResource, and it is deliberately
// ONE rule rather than a person's rule beside a stand-in for it.
//
// A resource records two things about who filed it. The SUBJECT is whoever made
// the call, which for a run is the principal script:<name>. The ADDRESS is
// PersonAddress at the time of the write: the person themselves, or the person
// an unattended caller acted for. Both sides of both comparisons must be
// non-empty -- absence of an identity is not a shared identity.
//
// The subject admits a caller acting as THEMSELVES only. A person's subject
// comes from the identity provider and names one person; a script principal is
// script:<name> and a script name is unique only within its OWNER
// (idx_scripts_name_owner, migration 000119), so two people who each keep a
// daily-sales would present the same subject, and matching an unattended caller
// on it would hand one person's automation the files the other's filed. The
// address is what identifies an unattended caller, and a run's own writes record
// it, so nothing is lost by reading only that for them.
//
// Reading the address makes the arm survive a MOVE. A move rewrites the library,
// the folder and the mcp:// address and never the uploader columns, so a person
// who uploaded a file goes on modifying it after it is filed into a persona
// library they merely belong to, which CanMoveToLibrary deliberately permits
// (#1576). Before that the uploader arm for an unattended caller required the
// file to be sitting in its own uploader's user library, so a person who moved a
// CSV their own scheduled script refreshes broke the schedule: nothing warned at
// the move, the schedule stayed enabled, and whether it broke turned on whether
// the author happened to be a platform administrator.
//
// Reading the address for the PERSON too, and not only for what acts for them,
// is what keeps the two from diverging the other way. A resource a run filed
// records the principal as its subject and the author as its address, so a
// person's own subject does not match it; once such a file leaves their library
// their scope authority ends with it, and an arm that read the address only for
// unattended callers would leave every OTHER script that person writes able to
// rewrite a file they cannot touch themselves.
//
// The address is folded for case, unlike the scope id isOwnScope compares: this
// reads a value off the row rather than a scope id the store lists by, so there
// is no listing predicate for it to disagree with. It matches the asset
// toolkit's ownsResource, which folds for the same reason.
//
// The grant is not re-derived from current authority, which is why
// CanAccessResource refuses it and uses the narrower uploadedBy instead: an
// administrator who uploaded into somebody else's scope and later lost the role
// keeps MODIFY authority over that row, and so does anything acting for them.
// That decay is unchanged; what changed is that it reaches the person and their
// scripts alike rather than whichever of the two the scope happened to favor.
func uploadedByCaller(c Claims, r *Resource) bool {
	if c.OnBehalfOf == "" && r.UploaderSub != "" && r.UploaderSub == c.Sub {
		return true
	}
	addr := PersonAddress(c)
	return addr != "" && r.UploaderEmail != "" && strings.EqualFold(r.UploaderEmail, addr)
}

func isPlatformAdmin(c Claims) bool {
	return c.IsAdmin ||
		slices.Contains(c.Roles, "admin") ||
		slices.Contains(c.Roles, "platform-admin")
}

func isPersonaAdmin(c Claims, personaName string) bool {
	if isPlatformAdmin(c) {
		return true
	}
	return slices.Contains(c.AdminOfPersonas, personaName) ||
		slices.Contains(c.Roles, "persona-admin:"+personaName)
}
