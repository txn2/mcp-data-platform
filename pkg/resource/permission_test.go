package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanWriteScope(t *testing.T) {
	admin := Claims{Sub: "admin-1", Roles: []string{"admin"}}
	user := Claims{Sub: "user-1", Roles: []string{"analyst"}}
	personaAdmin := Claims{Sub: "pa-1", Roles: []string{"persona-admin:finance"}}
	// Simulates an API key or OIDC user with prefixed role dp_admin mapped to admin persona.
	prefixedAdmin := Claims{Sub: "pa-2", Roles: []string{"dp_admin"}, IsAdmin: true}
	// Simulates a persona admin with a prefixed role.
	prefixedPersonaAdmin := Claims{Sub: "pa-3", Roles: []string{"dp_persona-admin:finance"}, AdminOfPersonas: []string{"finance"}}

	tests := []struct {
		name    string
		claims  Claims
		scope   Scope
		scopeID string
		want    bool
	}{
		{"admin writes global", admin, ScopeGlobal, "", true},
		{"admin writes persona", admin, ScopePersona, "finance", true},
		{"user cannot write global", user, ScopeGlobal, "", false},
		{"user writes own user scope", user, ScopeUser, "user-1", true},
		{"user cannot write other user scope", user, ScopeUser, "other", false},
		{"persona admin writes their persona", personaAdmin, ScopePersona, "finance", true},
		{"persona admin cannot write other persona", personaAdmin, ScopePersona, "engineering", false},
		{"prefixed role admin writes global via IsAdmin", prefixedAdmin, ScopeGlobal, "", true},
		{"prefixed role admin writes persona via IsAdmin", prefixedAdmin, ScopePersona, "finance", true},
		{"prefixed role admin writes user scope via IsAdmin", prefixedAdmin, ScopeUser, "other-user", true},
		{"prefixed persona admin writes their persona", prefixedPersonaAdmin, ScopePersona, "finance", true},
		{"prefixed persona admin cannot write other persona", prefixedPersonaAdmin, ScopePersona, "engineering", false},
		{"prefixed persona admin cannot write global", prefixedPersonaAdmin, ScopeGlobal, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanWriteScope(tt.claims, tt.scope, tt.scopeID)
			if got != tt.want {
				t.Errorf("CanWriteScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanModifyResource(t *testing.T) {
	r := &Resource{Scope: ScopePersona, ScopeID: "finance", UploaderSub: "uploader-1"}

	// Uploader can modify
	if !CanModifyResource(Claims{Sub: "uploader-1"}, r) {
		t.Error("uploader should be able to modify")
	}

	// Admin can modify
	if !CanModifyResource(Claims{Sub: "other", Roles: []string{"admin"}}, r) {
		t.Error("admin should be able to modify")
	}

	// Random user cannot modify
	if CanModifyResource(Claims{Sub: "random", Roles: []string{"analyst"}}, r) {
		t.Error("random user should not be able to modify")
	}
}

func TestCanReadResource(t *testing.T) {
	tests := []struct {
		name   string
		claims Claims
		res    *Resource
		want   bool
	}{
		{
			"global visible to all",
			Claims{Sub: "anyone"},
			&Resource{Scope: ScopeGlobal},
			true,
		},
		{
			"user visible to owner",
			Claims{Sub: "user-1"},
			&Resource{Scope: ScopeUser, ScopeID: "user-1"},
			true,
		},
		{
			"user not visible to other",
			Claims{Sub: "user-2"},
			&Resource{Scope: ScopeUser, ScopeID: "user-1"},
			false,
		},
		{
			"persona visible to member",
			Claims{Sub: "u1", Personas: []string{"finance"}},
			&Resource{Scope: ScopePersona, ScopeID: "finance"},
			true,
		},
		{
			"persona not visible to non-member",
			Claims{Sub: "u1", Personas: []string{"engineering"}},
			&Resource{Scope: ScopePersona, ScopeID: "finance"},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanReadResource(tt.claims, tt.res)
			if got != tt.want {
				t.Errorf("CanReadResource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVisibleScopes(t *testing.T) {
	claims := Claims{
		Sub:      "user-1",
		Email:    "user@example.com",
		Personas: []string{"finance", "analyst"},
	}
	scopes := VisibleScopes(claims)

	// Should have: global, user/user-1, user/user@example.com, persona/finance, persona/analyst
	if len(scopes) != 5 {
		t.Fatalf("expected 5 scopes, got %d: %v", len(scopes), scopes)
	}

	if scopes[0].Scope != ScopeGlobal {
		t.Errorf("first scope should be global, got %v", scopes[0])
	}
	if scopes[1].Scope != ScopeUser || scopes[1].ScopeID != "user-1" {
		t.Errorf("second scope should be user/user-1, got %v", scopes[1])
	}
	if scopes[2].Scope != ScopeUser || scopes[2].ScopeID != "user@example.com" {
		t.Errorf("third scope should be user/user@example.com, got %v", scopes[2])
	}
}

func TestVisibleScopes_EmailSameAsSub(t *testing.T) {
	// When email equals sub, don't duplicate.
	claims := Claims{Sub: "same", Email: "same"}
	scopes := VisibleScopes(claims)
	userScopes := 0
	for _, s := range scopes {
		if s.Scope == ScopeUser {
			userScopes++
		}
	}
	if userScopes != 1 {
		t.Errorf("expected 1 user scope, got %d", userScopes)
	}
}

func TestIsPlatformAdmin(t *testing.T) {
	if !isPlatformAdmin(Claims{Roles: []string{"admin"}}) {
		t.Error("admin role should be platform admin")
	}
	if !isPlatformAdmin(Claims{Roles: []string{"platform-admin"}}) {
		t.Error("platform-admin role should be platform admin")
	}
	if isPlatformAdmin(Claims{Roles: []string{"analyst"}}) {
		t.Error("analyst should not be platform admin")
	}
	// IsAdmin flag set by caller based on persona resolution — works
	// regardless of role name (e.g., dp_admin, custom_superuser).
	if !isPlatformAdmin(Claims{Roles: []string{"dp_admin"}, IsAdmin: true}) {
		t.Error("IsAdmin=true should be platform admin regardless of role name")
	}
	if !isPlatformAdmin(Claims{IsAdmin: true}) {
		t.Error("IsAdmin=true with no roles should still be platform admin")
	}
	if isPlatformAdmin(Claims{Roles: []string{"dp_analyst"}, IsAdmin: false}) {
		t.Error("IsAdmin=false with non-admin role should not be platform admin")
	}
}

func TestIsPersonaAdmin(t *testing.T) {
	if !isPersonaAdmin(Claims{Roles: []string{"persona-admin:finance"}}, "finance") {
		t.Error("persona-admin:finance should be admin of finance")
	}
	if isPersonaAdmin(Claims{Roles: []string{"persona-admin:finance"}}, "engineering") {
		t.Error("persona-admin:finance should not be admin of engineering")
	}
	if !isPersonaAdmin(Claims{Roles: []string{"admin"}}, "anything") {
		t.Error("platform admin should be persona admin of any persona")
	}
	// AdminOfPersonas resolved by caller — works with prefixed roles.
	if !isPersonaAdmin(Claims{Roles: []string{"dp_persona-admin:finance"}, AdminOfPersonas: []string{"finance"}}, "finance") {
		t.Error("AdminOfPersonas should grant persona admin for prefixed roles")
	}
	if isPersonaAdmin(Claims{Roles: []string{"dp_persona-admin:finance"}, AdminOfPersonas: []string{"finance"}}, "engineering") {
		t.Error("AdminOfPersonas for finance should not grant admin for engineering")
	}
}

// An admin who uploads a persona-scoped resource must be able to read, edit, and
// delete it. VisibleScopes is membership-based and grants no cross-persona read,
// so the by-id handlers gate on CanAccessResource: without it an admin could
// create material they could neither manage nor remove.
func TestCanAccessResource(t *testing.T) {
	admin := Claims{Sub: "admin-1", Email: "admin@example.com", Personas: []string{"admin"}, IsAdmin: true}
	personaAdmin := Claims{Sub: "pa-1", Roles: []string{"dp_persona-admin:finance"}, AdminOfPersonas: []string{"finance"}}
	member := Claims{Sub: "u-1", Email: "u1@example.com", Personas: []string{"analyst"}}

	personaRes := &Resource{Scope: ScopePersona, ScopeID: "analyst", UploaderSub: "admin-1"}
	financeRes := &Resource{Scope: ScopePersona, ScopeID: "finance", UploaderSub: "someone"}
	otherUserRes := &Resource{Scope: ScopeUser, ScopeID: "u-2", UploaderSub: "u-2"}
	// Uploaded by the admin INTO another user's scope, which only CanWriteScope
	// permits; uploader_sub keeps naming the admin after their role is revoked.
	adminUploadedToUser := &Resource{Scope: ScopeUser, ScopeID: "u-2", UploaderSub: "admin-1"}
	ownRes := &Resource{Scope: ScopeUser, ScopeID: "u-1", UploaderSub: "u-1"}

	// An admin who uploaded into another user's scope and then lost the admin role:
	// the uploader_sub on the row still names them, but their authority is gone.
	exAdmin := Claims{Sub: "admin-1", Email: "admin@example.com", Personas: []string{"viewer"}}

	tests := []struct {
		name   string
		claims Claims
		res    *Resource
		want   bool
	}{
		{"admin reaches a persona resource outside their own persona", admin, personaRes, true},
		{"admin reaches the user resource they uploaded", admin, adminUploadedToUser, true},
		{"a former admin loses access to material they uploaded", exAdmin, adminUploadedToUser, false},
		{"a former admin loses access to the persona material they uploaded", exAdmin, personaRes, false},
		{"admin reaches another user's resource", admin, otherUserRes, true},
		{"persona admin reaches their persona's resource", personaAdmin, financeRes, true},
		{"persona admin does not reach another persona's resource", personaAdmin, personaRes, false},
		{"member reaches their own persona's resource", member, personaRes, true},
		{"member reaches their own user resource", member, ownRes, true},
		{"member does not reach another user's resource", member, otherUserRes, false},
		{"member does not reach a persona they do not belong to", member, financeRes, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanAccessResource(tt.claims, tt.res); got != tt.want {
				t.Errorf("CanAccessResource = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Acting on somebody else's behalf (#1419, #1487) ---

// scriptRun is the claims a managed-script run carries: a principal that owns
// nothing, the script OWNER's address (carried for accountability), and the
// version AUTHOR's address as the person the run acts for. The two are the same
// person until an administrator transfers the script, which rewrites the version
// under their own authorship and leaves somebody else the owner.
func scriptRun(owner, author string) Claims {
	return BuildClaims("script:weekly-refresh", owner, "analyst", []string{"analyst"}, false).ActingFor(author)
}

// ownRun is the ordinary case: the script's owner wrote its current version.
func ownRun(author string) Claims { return scriptRun(author, author) }

func TestBuildClaimsCarriesNoOneByDefault(t *testing.T) {
	c := BuildClaims("sub-1", "a@example.com", "analyst", []string{"analyst"}, false)

	assert.Empty(t, c.OnBehalfOf, "a person acts as themselves; the address must stay inert for them")
}

func TestVisibleScopesIncludeTheLibraryOfThePersonActedFor(t *testing.T) {
	// A transferred script: owned by one person, authored by another, and it
	// acts for the author. Only the author's library is reachable, which is the
	// distinction a run whose owner and author agree cannot show.
	scopes := VisibleScopes(scriptRun("owner@example.com", "author@example.com"))

	assert.Contains(t, scopes, ScopeFilter{Scope: ScopeUser, ScopeID: "author@example.com"},
		"a run's own writes land in its author's library, so it has to be able to see it")
	assert.NotContains(t, scopes, ScopeFilter{Scope: ScopeUser, ScopeID: "owner@example.com"},
		"the owner's address is carried for accountability; reading their library on it would pair "+
			"the author's authority with the owner's ownership")
	assert.Contains(t, scopes, ScopeFilter{Scope: ScopeUser, ScopeID: "script:weekly-refresh"})
}

func TestATransferredRunActsForItsAuthorAndNotItsOwner(t *testing.T) {
	c := scriptRun("owner@example.com", "author@example.com")

	assert.True(t, CanWriteScope(c, ScopeUser, "author@example.com"),
		"the run presents the author's roles, so it writes into the author's library")
	assert.False(t, CanWriteScope(c, ScopeUser, "owner@example.com"),
		"the owner is who may trigger it, not whose authority it carries")
	assert.True(t, CanModifyResource(c, &Resource{
		Scope: ScopeUser, ScopeID: "sub-x", UploaderSub: "sub-x", UploaderEmail: "author@example.com",
	}))
	assert.False(t, CanModifyResource(c, &Resource{
		Scope: ScopeUser, ScopeID: "sub-y", UploaderSub: "sub-y", UploaderEmail: "owner@example.com",
	}))
}

func TestVisibleScopesDoNotRepeatOneAddress(t *testing.T) {
	c := BuildClaims("sub-1", "a@example.com", "", nil, false).ActingFor("a@example.com")

	scopes := VisibleScopes(c)

	seen := map[ScopeFilter]int{}
	for _, sf := range scopes {
		seen[sf]++
	}
	assert.Equal(t, 1, seen[ScopeFilter{Scope: ScopeUser, ScopeID: "a@example.com"}])
}

func TestCanWriteScopeForThePersonActedFor(t *testing.T) {
	c := ownRun("author@example.com")

	assert.True(t, CanWriteScope(c, ScopeUser, "author@example.com"))
	assert.False(t, CanWriteScope(c, ScopeUser, "AUTHOR@example.com"),
		"a scope id is compared exactly, because that is how the listing predicate compares it: "+
			"folding here alone would make a resource modifiable by somebody it never lists for")
	assert.False(t, CanWriteScope(c, ScopeUser, "someone-else@example.com"))
	assert.False(t, CanWriteScope(c, ScopeGlobal, ""),
		"acting for somebody grants their scope, not administrator authority")
}

func TestAnAbsentAddressMatchesNothing(t *testing.T) {
	// A human caller with no on-behalf-of address must not match a resource or
	// a scope that also records none.
	c := BuildClaims("", "", "", nil, false)

	assert.False(t, CanWriteScope(c, ScopeUser, ""))
	assert.False(t, CanAccessResource(c, &Resource{Scope: ScopeUser, ScopeID: "x"}))
	assert.False(t, CanModifyResource(c, &Resource{Scope: ScopeUser, ScopeID: "x"}))
	assert.NotContains(t, VisibleScopes(c), ScopeFilter{Scope: ScopeUser, ScopeID: ""})
}

// TestARunReachesTheFileItsAuthorUploaded is the case the address exists for: a
// person uploads through the portal, which files the resource under their SUB,
// and their own script then has to be able to read and replace it.
func TestARunReachesTheFileItsAuthorUploaded(t *testing.T) {
	uploaded := &Resource{
		Scope: ScopeUser, ScopeID: "sub-of-author",
		UploaderSub: "sub-of-author", UploaderEmail: "author@example.com",
	}
	run := ownRun("author@example.com")

	assert.True(t, CanAccessResource(run, uploaded),
		"a run refused its author's own file cannot be said to act for them")
	assert.True(t, CanModifyResource(run, uploaded))

	// Another person's script reaches neither.
	other := ownRun("someone-else@example.com")
	assert.False(t, CanAccessResource(other, uploaded))
	assert.False(t, CanModifyResource(other, uploaded))
}

func TestARunReachesTheFileItWroteItself(t *testing.T) {
	written := &Resource{
		Scope: ScopeUser, ScopeID: "author@example.com",
		UploaderSub: "script:weekly-refresh", UploaderEmail: "author@example.com",
	}
	run := ownRun("author@example.com")

	assert.True(t, CanReadResource(run, written))
	assert.True(t, CanModifyResource(run, written))
}

func TestAResourceWithNoRecordedUploaderAddressIsNotAnyonesToChange(t *testing.T) {
	orphan := &Resource{Scope: ScopeUser, ScopeID: "sub-of-author", UploaderSub: "sub-of-author"}

	assert.False(t, CanAccessResource(ownRun("author@example.com"), orphan),
		"an absent address is not a shared identity")
}

// TestThePersonCanManageWhatTheirScriptFiled is the other half of a run writing
// into its author's library: the file has to be theirs to edit and delete, or a
// scheduled script produces material only a platform administrator can ever
// remove.
func TestThePersonCanManageWhatTheirScriptFiled(t *testing.T) {
	const author = "author@example.com"
	// What a run's create writes: the author's library, the principal as
	// uploader subject, the author as the recorded address.
	filed := &Resource{
		Scope: ScopeUser, ScopeID: author,
		UploaderSub: "script:weekly-refresh", UploaderEmail: author,
	}
	human := Claims{Sub: "sub-of-author", Email: author}

	assert.True(t, CanReadResource(human, filed))
	assert.True(t, CanAccessResource(human, filed))
	assert.True(t, CanModifyResource(human, filed),
		"a library named by address is one its owner can see; it has to be one they can manage")
	assert.True(t, CanWriteScope(human, ScopeUser, author))

	stranger := Claims{Sub: "sub-of-stranger", Email: "stranger@example.com"}
	assert.False(t, CanAccessResource(stranger, filed))
	assert.False(t, CanModifyResource(stranger, filed))
}

// TestARunDoesNotOutliveItsAuthorsOwnReach is the limit on the uploader arm for
// VISIBILITY. An administrator who uploaded into somebody else's library and
// later lost the role is refused sight of that file; their script must be
// refused it too, or the script sees strictly more than the person it acts for.
//
// Modification is the other half and is deliberately not symmetric with it. The
// uploader arm of CanModifyResource is never re-derived from current authority,
// so the person keeps it on that row -- and the run therefore keeps it too. One
// grant with one holder is the property; a run refused where the person is
// admitted is the same authority nobody has, inverted (#1576). In practice the
// run still cannot replace this file, because resourcewrite reads it through
// CanAccessResource before it asks whether it may be modified.
func TestARunDoesNotOutliveItsAuthorsOwnReach(t *testing.T) {
	victimFile := &Resource{
		Scope: ScopeUser, ScopeID: "bob-sub",
		UploaderSub: "ex-admin-sub", UploaderEmail: "ex-admin@example.com",
	}
	exAdmin := Claims{Sub: "ex-admin-sub", Email: "ex-admin@example.com"}
	require.False(t, CanAccessResource(exAdmin, victimFile),
		"the premise: the person themselves is already refused sight of it")
	require.True(t, CanModifyResource(exAdmin, victimFile),
		"the premise: the uploader arm is not re-derived, so the person keeps modify on the row")

	run := ownRun("ex-admin@example.com")

	assert.False(t, CanAccessResource(run, victimFile),
		"a run seeing what its author cannot is an authority nobody has")
	assert.True(t, CanModifyResource(run, victimFile),
		"and refusing the run where the person is admitted is that same authority, inverted")
}

// TestARunReachesAFileInItsAuthorsOwnLibrary is the case the uploader arm exists
// for, and the one the limit above must not take with it.
// The uploader address IS folded, because it is read off the row rather than
// listed by, so there is no predicate for it to disagree with.
func TestTheUploaderAddressIsMatchedCaseInsensitively(t *testing.T) {
	own := &Resource{
		Scope: ScopeUser, ScopeID: "author-sub",
		UploaderSub: "author-sub", UploaderEmail: "Author@Example.com",
	}

	assert.True(t, CanModifyResource(ownRun("author@example.com"), own))
}

func TestARunReachesAFileInItsAuthorsOwnLibrary(t *testing.T) {
	own := &Resource{
		Scope: ScopeUser, ScopeID: "author-sub",
		UploaderSub: "author-sub", UploaderEmail: "author@example.com",
	}

	run := ownRun("author@example.com")

	assert.True(t, CanAccessResource(run, own))
	assert.True(t, CanModifyResource(run, own))
}

// --- A move does not revoke the script that maintains the file (#1576) ---

// movedFiles are the same uploaded file after each move CanMoveToLibrary
// permits: into the global library, and into a persona library. A move rewrites
// the library, the folder and the mcp:// address and never the uploader
// columns, which is why the row still names the person who uploaded it.
func movedFiles() []*Resource {
	return []*Resource{
		{Scope: ScopeGlobal, UploaderSub: "author-sub", UploaderEmail: "author@example.com"},
		{Scope: ScopePersona, ScopeID: "finance", UploaderSub: "author-sub", UploaderEmail: "author@example.com"},
	}
}

// TestAMoveDoesNotRevokeTheScriptThatMaintainsTheFile is #1576. A person moves a
// CSV their own scheduled script refreshes into a library they belong to, which
// CanMoveToLibrary deliberately permits, and the script has to go on refreshing
// it: the person may still replace the content, so the script acting for them
// may too.
func TestAMoveDoesNotRevokeTheScriptThatMaintainsTheFile(t *testing.T) {
	author := Claims{Sub: "author-sub", Email: "author@example.com"}
	run := ownRun("author@example.com")

	for _, r := range movedFiles() {
		require.True(t, CanModifyResource(author, r),
			"the premise: the move left the person able to replace the content: %s", r.Scope)
		assert.True(t, CanModifyResource(run, r),
			"the person's script must reach what the person reaches: %s", r.Scope)
	}
}

// TestAMovedFileIsNoOneElsesScriptToChange is the other side of the same rule: a
// run whose author never uploaded the file and holds no authority over the
// library it sits in is refused, and so is that author.
func TestAMovedFileIsNoOneElsesScriptToChange(t *testing.T) {
	stranger := Claims{Sub: "stranger-sub", Email: "stranger@example.com"}
	strangerRun := ownRun("stranger@example.com")

	for _, r := range movedFiles() {
		require.False(t, CanModifyResource(stranger, r),
			"the premise: the person themselves may not replace it: %s", r.Scope)
		assert.False(t, CanModifyResource(strangerRun, r),
			"a script reaching what its author cannot is an authority nobody has: %s", r.Scope)
	}
}

// TestAFileAScriptFiledIsTheAuthorsToChangeWherever holds the uploader arm
// symmetric on the row where the two holders record differently. A run's create
// writes the PRINCIPAL as the subject and the author as the address, so the
// author's own subject does not match it and their scope authority is all they
// have -- which the move takes away. Reading the address for the person as well
// as for what acts for them is what keeps every other script that person writes
// from reaching a file they cannot touch themselves.
func TestAFileAScriptFiledIsTheAuthorsToChangeWherever(t *testing.T) {
	const author = "author@example.com"
	// Filed by one script into the author's library, then published to everyone.
	filed := &Resource{
		Scope: ScopeGlobal, UploaderSub: "script:weekly-refresh", UploaderEmail: author,
	}
	person := Claims{Sub: "author-sub", Email: author, Personas: []string{"analyst"}}
	sibling := BuildClaims("script:monthly-rollup", author, "analyst", []string{"analyst"}, false).
		ActingFor(author)

	assert.True(t, CanModifyResource(person, filed),
		"the person whose authority filed it is who may change it, wherever it is filed")
	assert.True(t, CanModifyResource(sibling, filed),
		"and their other scripts reach exactly that, neither more nor less")

	stranger := Claims{Sub: "stranger-sub", Email: "stranger@example.com"}
	assert.False(t, CanModifyResource(stranger, filed))
	assert.False(t, CanModifyResource(ownRun("stranger@example.com"), filed),
		"a script principal is script:<name> and a name is unique only within its owner, "+
			"so two people's daily-sales present one subject; the address is what tells them apart")
}

// TestOneScriptNameIsNotOneIdentity states that last point on its own, on the
// row where the two collide: the resource was filed by the author's script, and
// somebody else's script of the SAME NAME presents the same principal.
func TestOneScriptNameIsNotOneIdentity(t *testing.T) {
	filed := &Resource{
		Scope: ScopeGlobal,
		// What Script.Principal() produces, and what a run's create records.
		UploaderSub: "script:weekly-refresh", UploaderEmail: "author@example.com",
	}

	require.True(t, CanModifyResource(ownRun("author@example.com"), filed),
		"the premise: the author's own run reaches it")
	assert.False(t, CanModifyResource(ownRun("somebody-else@example.com"), filed),
		"another person's script of the same name is another person's script")
}

// TestAMoveWidensNothingARunCanSee is criterion 4. CanAccessResource keeps the
// scope-bound uploader arm, so the modify predicate admits nothing to the
// visibility gate. The persona library here is one the run does not belong to;
// what a move can actually reach -- a persona the author is a member of, and
// the global library -- is readable on its own terms and needs no uploader arm.
func TestAMoveWidensNothingARunCanSee(t *testing.T) {
	run := ownRun("author@example.com")
	elsewhere := &Resource{
		Scope: ScopePersona, ScopeID: "finance",
		UploaderSub: "author-sub", UploaderEmail: "author@example.com",
	}

	assert.False(t, CanAccessResource(run, elsewhere),
		"a persona library the author does not belong to stays out of sight")
}

// TestTheModifyArmNeverMatchesAnAbsentIdentity is criterion 5: a run acting for
// nobody, a caller with no address at all, and a row recording no uploader are
// not the same party.
func TestTheModifyArmNeverMatchesAnAbsentIdentity(t *testing.T) {
	noUploader := &Resource{Scope: ScopeGlobal, UploaderSub: "author-sub"}
	assert.False(t, CanModifyResource(ownRun("author@example.com"), noUploader),
		"a row with no recorded uploader address is nobody's to change")

	uploaded := &Resource{
		Scope: ScopeGlobal, UploaderSub: "author-sub", UploaderEmail: "author@example.com",
	}
	forNobody := BuildClaims("script:weekly-refresh", "", "analyst", []string{"analyst"}, false)
	assert.False(t, CanModifyResource(forNobody, uploaded),
		"a run acting for nobody matches nobody")
	assert.False(t, CanModifyResource(Claims{}, uploaded),
		"and neither does a caller with no identity at all")
	assert.False(t, CanModifyResource(Claims{}, &Resource{Scope: ScopeGlobal}),
		"two absences are not one identity")
}

// TestTheModifyArmFoldsCaseAfterAMove holds the folding to the same rule the
// uploader arm has always applied, on a row a move took out of its own library.
func TestTheModifyArmFoldsCaseAfterAMove(t *testing.T) {
	moved := &Resource{
		Scope: ScopeGlobal, UploaderSub: "author-sub", UploaderEmail: "Author@Example.com",
	}

	assert.True(t, CanModifyResource(ownRun("author@example.com"), moved))
}

// --- The listing predicate (#1553) ---

// admin1553 is a platform administrator who belongs to one persona and no other.
func admin1553() Claims {
	return Claims{Sub: "admin-sub", Email: "admin@example.com", Personas: []string{"admin"}, IsAdmin: true}
}

// member1553 is an ordinary person in one persona, holding no admin role.
func member1553() Claims {
	return Claims{Sub: "member-sub", Email: "member@example.com", Personas: []string{"analyst"}}
}

func TestListScopesUnnarrowed(t *testing.T) {
	scopes, all := ListScopes(admin1553(), "", "")
	assert.True(t, all, "an administrator's unfiltered listing spans every library")
	assert.Empty(t, scopes, "an unrestricted listing needs no scope predicate")

	scopes, all = ListScopes(member1553(), "", "")
	assert.False(t, all, "an ordinary caller's listing is never unrestricted")
	assert.ElementsMatch(t, VisibleScopes(member1553()), scopes)
}

func TestListScopesNarrowedToALibraryTheCallerIsNotIn(t *testing.T) {
	// The case the change exists for: an administrator uploads into a persona
	// they do not belong to, which CanWriteScope permits, and must then be able
	// to list it.
	scopes, all := ListScopes(admin1553(), "persona", "finance")
	assert.False(t, all)
	assert.Equal(t, []ScopeFilter{{Scope: ScopePersona, ScopeID: "finance"}}, scopes)

	// The same request by somebody with no authority over that persona narrows
	// to nothing, which the store reads as a listing with no rows.
	scopes, all = ListScopes(member1553(), "persona", "finance")
	assert.False(t, all)
	assert.Empty(t, scopes)
}

func TestListScopesNarrowedToAPersonaAdminsOwnPersona(t *testing.T) {
	// A persona administrator's authority over the library is the role, not
	// membership, so their listing of it is the library itself.
	c := Claims{
		Sub: "curator-sub", Email: "curator@example.com",
		Roles: []string{"dp_persona-admin:finance"}, AdminOfPersonas: []string{"finance"},
	}
	scopes, all := ListScopes(c, "persona", "finance")
	assert.False(t, all)
	assert.Equal(t, []ScopeFilter{{Scope: ScopePersona, ScopeID: "finance"}}, scopes)
}

func TestListScopesNarrowedToTheCallersOwnMemberships(t *testing.T) {
	scopes, all := ListScopes(member1553(), "persona", "analyst")
	assert.False(t, all)
	assert.Equal(t, []ScopeFilter{{Scope: ScopePersona, ScopeID: "analyst"}}, scopes)

	scopes, _ = ListScopes(member1553(), "user", "member-sub")
	assert.Equal(t, []ScopeFilter{{Scope: ScopeUser, ScopeID: "member-sub"}}, scopes)

	scopes, _ = ListScopes(member1553(), "global", "")
	assert.Equal(t, []ScopeFilter{{Scope: ScopeGlobal}}, scopes)
}

func TestListScopesWithAScopeKindAndNoLibrary(t *testing.T) {
	// "persona" with no persona names a kind rather than a library, so even an
	// administrator gets the memberships they hold rather than every persona:
	// there is no set of pairs that says "every persona" and no roster to
	// build one from.
	scopes, all := ListScopes(admin1553(), "persona", "")
	assert.False(t, all)
	assert.Equal(t, []ScopeFilter{{Scope: ScopePersona, ScopeID: "admin"}}, scopes)
}

func TestListScopesRefusesAnUnknownScope(t *testing.T) {
	scopes, all := ListScopes(admin1553(), "bogus", "anything")
	assert.False(t, all)
	assert.Empty(t, scopes, "a scope that names no kind of library lists nothing")
}

func TestCanSeeLibrary(t *testing.T) {
	admin, member := admin1553(), member1553()

	assert.True(t, CanSeeLibrary(member, ScopeFilter{Scope: ScopeGlobal}))
	assert.True(t, CanSeeLibrary(member, ScopeFilter{Scope: ScopePersona, ScopeID: "analyst"}))
	assert.True(t, CanSeeLibrary(member, ScopeFilter{Scope: ScopeUser, ScopeID: "member-sub"}))
	assert.False(t, CanSeeLibrary(member, ScopeFilter{Scope: ScopePersona, ScopeID: "finance"}))
	assert.False(t, CanSeeLibrary(member, ScopeFilter{Scope: ScopeUser, ScopeID: "someone-else"}))

	assert.True(t, CanSeeLibrary(admin, ScopeFilter{Scope: ScopePersona, ScopeID: "finance"}),
		"write authority over a library is authority to read it")
	assert.True(t, CanSeeLibrary(admin, ScopeFilter{Scope: ScopeUser, ScopeID: "someone-else"}))
}
