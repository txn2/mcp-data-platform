package portaldomain

import "strings"

// AnonymousOwner is the identifier a write records when the request carried no
// authenticated identity. It is a placeholder for "nobody", not a person: two
// callers that both resolve to it are not the same caller, and an entity
// recorded under it belongs to no one, so no ownership arm may ever match on
// it.
const AnonymousOwner = "anonymous"

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
}

// NewAssetOwner builds an ownership identity, discarding an identifier that
// names nobody. Whitespace is trimmed and the anonymous sentinel is dropped, so
// a caller that never authenticated carries no key to match on rather than a
// key shared with every other such caller.
func NewAssetOwner(userID, email string) AssetOwner {
	return AssetOwner{UserID: ownerKey(userID), Email: ownerKey(email)}
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

// Identified reports whether this identity carries anything to match on. A
// caller with neither identifier owns nothing, which is what a surface checks
// before scoping a listing rather than returning the whole table.
func (o AssetOwner) Identified() bool { return o.UserID != "" || o.Email != "" }

// EmailKey returns the address the ownership arm may be matched on, or "" when
// there is none. It is what a SQL predicate binds, so the anonymous sentinel is
// never a parameter of a query.
func (o AssetOwner) EmailKey() string { return ownerKey(o.Email) }

// Owns reports whether an asset recorded under (ownerID, ownerEmail) belongs to
// this identity.
//
// It matches on either identifier. Both sides of an arm must name somebody:
// absence of an identity is not a shared identity, so an unattributed row is
// not owned by an unauthenticated caller. The address comparison is
// case-insensitive, matching the share-recipient and prompt-ownership
// comparisons, because addresses reach the platform from several identity
// providers.
func (o AssetOwner) Owns(ownerID, ownerEmail string) bool {
	if id := ownerKey(ownerID); id != "" && id == o.UserID {
		return true
	}
	email := ownerKey(ownerEmail)
	return email != "" && o.Email != "" && strings.EqualFold(email, o.Email)
}

// OwnsAsset is Owns for callers holding the record itself.
func (o AssetOwner) OwnsAsset(a *Asset) bool {
	return a != nil && o.Owns(a.OwnerID, a.OwnerEmail)
}
