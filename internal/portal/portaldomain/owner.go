package portaldomain

import "strings"

// AnonymousOwner is the identifier a write records when the request carried no
// authenticated identity. It is a placeholder for "nobody", not a person: two
// callers that both resolve to it are not the same caller, and an entity
// recorded under it belongs to no one, so no ownership arm may ever match on
// it.
const AnonymousOwner = "anonymous"

// ownerMatch is how an identity's two identifiers combine into a test against a
// stored row. The zero value is a person, so a bare AssetOwner literal is the
// identity every human caller carries.
type ownerMatch uint8

const (
	// matchEither is a caller acting as themselves. The subject comes from an
	// identity provider and the address is their own, so each names that one
	// person and a row recording either is theirs.
	matchEither ownerMatch = iota
	// matchAddress is an unattended caller judged as the person it acts for.
	// Its subject is dropped: Script.Principal() is script:<name> and
	// idx_scripts_name_owner is UNIQUE (owner_email, name), so a script name is
	// unique only within its OWNER and two people who each keep a daily-sales
	// present the same subject (#1579).
	matchAddress
)

// AssetOwner is who an asset belongs to, held as the two identifiers the row
// records: the user id the write authenticated as, and the address of the
// person the write was made for.
//
// Both are needed because they can name different people. A managed-script run
// authenticates as script:<name> and stamps that principal on everything it
// writes, while owner_email carries the address of the person who owns the
// script. Judging ownership on the id alone therefore hides a run's output from
// the only person it was produced for, and leaves it reachable by nobody but an
// administrator (#1551). Judging it on the address alone would lose every asset
// whose owner_email is blank, which is what rows written before owner_email
// existed look like.
//
// How the two combine is not the same for every caller, which is what
// ownerMatch carries. A script principal is not unique to one person, so it is
// never an arm of its own: matched alone it would hand a run of one person's
// daily-sales the outputs of another person's daily-sales (#1579).
//
// The same value describes a caller and a stored row, and Owns is the one
// comparison between them, so "this asset is this person's" means the same
// thing at every surface that asks: the portal's list and access checks, the
// ranked search, the MCP tool, discovery, and table registration.
type AssetOwner struct {
	// UserID is the identity the caller authenticated as.
	UserID string
	// Email is the address of the person the caller is acting as. For a
	// person that is their own address; for a managed-script run it is the
	// address the run acts for, which is what makes the run reach what that
	// person reaches without also carrying their id.
	Email string
	// match is how the two combine. Unexported so a caller states which kind
	// of identity it holds through a constructor rather than assembling a
	// combination by hand.
	match ownerMatch
}

// NewAssetOwner builds the ownership identity of a caller acting as themselves,
// discarding an identifier that names nobody. Whitespace is trimmed and the
// anonymous sentinel is dropped, so a caller that never authenticated carries
// no key to match on rather than a key shared with every other such caller.
func NewAssetOwner(userID, email string) AssetOwner {
	return AssetOwner{UserID: ownerKey(userID), Email: ownerKey(email), match: matchEither}
}

// ActingFor returns the identity of an unattended caller acting for the person
// at address: the address alone, because the subject such a caller presents is
// not unique to one person.
//
// An empty address is a no-op, so a surface can pass whatever its context
// carries without first asking whether the caller is unattended. That is the
// same contract as resource.Claims.ActingFor, which the resource surface reads
// for the same reason (#1576).
//
// A run's own writes record the address beside the principal, so for every
// script that has an owner the address matches what the subject would have.
// The exception is a script whose owner_email is blank -- the shape migration
// 000119 describes, authored by a principal carrying no address, which RefuseRun
// does not refuse and which the transfer action exists to give an owner. Its
// outputs record the shared principal and nothing else, so no person owns them
// and this identity does not either; an administrator reaches them through the
// admin arm of each check, which was already the only way in (#1551).
func (o AssetOwner) ActingFor(address string) AssetOwner {
	address = ownerKey(address)
	if address == "" {
		return o
	}
	o.Email, o.match = address, matchAddress
	return o
}

// ownerKey normalizes one identifier to the value it may be matched on, or ""
// when it names nobody.
func ownerKey(v string) string {
	v = strings.TrimSpace(v)
	if v == AnonymousOwner {
		return ""
	}
	return v
}

// OwnerArms is how an ownership predicate must compare a stored row's
// (owner_id, owner_email) to an identity: the owner id to match and the address
// to match. An empty identifier is not an arm, and an OwnerArms with neither
// matches nothing.
//
// It exists so that Owns and the two SQL renderings of the same judgment -- the
// squirrel predicate a listing binds and the raw statement the ranked search
// numbers its own placeholders for -- cannot drift. What a caller may see in a
// listing and what a caller may act on have to name the same rows.
type OwnerArms struct {
	// UserID is the owner id to compare, or "" when the subject is not an arm.
	UserID string
	// Email is the address to compare case-insensitively, or "" when the
	// address is not an arm.
	Email string
}

// Arms returns the comparison this identity is matched by.
func (o AssetOwner) Arms() OwnerArms {
	id, email := ownerKey(o.UserID), ownerKey(o.Email)
	if o.match == matchAddress {
		return OwnerArms{Email: email}
	}
	return OwnerArms{UserID: id, Email: email}
}

// Identified reports whether this identity carries anything to match on. A
// caller with nothing to match owns nothing, which is what a surface checks
// before scoping a listing rather than returning the whole table. It reads the
// arms rather than the fields, so an identity holding an identifier it may not
// be matched on is not mistaken for one that can be.
func (o AssetOwner) Identified() bool {
	a := o.Arms()
	return a.UserID != "" || a.Email != ""
}

// Owns reports whether an asset recorded under (ownerID, ownerEmail) belongs to
// this identity.
//
// Both sides of an arm must name somebody: absence of an identity is not a
// shared identity, so an unattributed row is not owned by an unauthenticated
// caller. The address comparison is case-insensitive, matching the
// share-recipient and prompt-ownership comparisons, because addresses reach the
// platform from several identity providers.
func (o AssetOwner) Owns(ownerID, ownerEmail string) bool {
	a := o.Arms()
	id, email := ownerKey(ownerID), ownerKey(ownerEmail)
	byID := a.UserID != "" && id == a.UserID
	byEmail := a.Email != "" && email != "" && strings.EqualFold(email, a.Email)
	return byID || byEmail
}

// OwnsAsset is Owns for callers holding the record itself.
func (o AssetOwner) OwnsAsset(a *Asset) bool {
	return a != nil && o.Owns(a.OwnerID, a.OwnerEmail)
}
