// Package knowledge is the unified read path for platform knowledge (#632).
//
// The platform holds knowledge in several stores that an agent must today
// search separately (captured memory, reviewed insights, managed assets, and
// later the technical catalog and prompts). Each store has its own tool, its
// own scope rules, and its own relevance scoring, so the agent pays a discovery
// tax to find anything and usually declines to pay it.
//
// This package collapses those stores behind one Provider interface and a
// Router that fans a single query across every registered provider, normalizes
// each provider's local relevance score onto a common scale, fuses the results
// into one ranked list, and enforces per-user scope so a shared search can
// never surface one user's private records to another.
//
// The same Router is exposed two ways from one code path: as the
// search agent tool (pull), and later as a retriever wired into the
// enrichment middleware (push). PR1 (#632) builds the pull path with the
// memory, insights, and assets providers; the technical catalog (datahub) and
// prompt providers, and push injection, land in follow-up PRs.
package knowledge

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/query"
)

// Scope declares whether a provider's records are visible to every caller or
// only to the caller who owns them. The Router uses it to decide which
// providers a request may touch and with what identity.
type Scope int

const (
	// ScopeShared marks a provider that is queried for every request, with or
	// without a caller identity, because it can always return at least some
	// content visible to everyone (the technical catalog, global prompts). A
	// shared provider may still use the caller identity to widen what it
	// returns (a prompt provider adds the caller's persona/personal prompts to
	// the global ones); "shared" means "always queried", not "ignores the
	// caller". It must never return another caller's private records.
	ScopeShared Scope = iota

	// ScopePerUser marks a provider whose records belong to individual
	// callers (personal memory, personal assets). The Router queries a
	// per-user provider only when the request carries the identity that
	// provider scopes on, and the provider must restrict results to that
	// identity. This is the security boundary that keeps one user's private
	// records out of another user's search.
	ScopePerUser
)

// String renders a Scope for logs and test failures.
func (s Scope) String() string {
	switch s {
	case ScopeShared:
		return "shared"
	case ScopePerUser:
		return "per_user"
	default:
		return "unknown"
	}
}

// Caller is the resolved identity of the requester. Per-user providers scope on
// it, and they do not all key on the same field: captured memory and insights
// are owned by email (memory_records.created_by), while managed assets are
// owned by the user's UUID (assets.owner_id). Both fields travel on every
// request so each provider selects the one it scopes on; a provider whose key
// is empty must return no results rather than query unscoped.
type Caller struct {
	// UserID is the caller's UUID identity, the owner key for assets.
	UserID string
	// Email is the caller's canonical identity, the owner key for memory
	// and insights.
	Email string
	// Persona is the caller's resolved persona. It scopes entity-keyed memory
	// lookups and selects which persona-scoped prompts are visible. It is not a
	// security boundary on its own (per-user records are scoped by Email/UserID);
	// it narrows persona-targeted content.
	Persona string
	// Personas is every persona the caller BELONGS TO, derived from their roles.
	// It is deliberately distinct from Persona: Persona says "which persona is
	// this request acting as" and can be set explicitly on the request, while
	// this set says "which personas is this caller a member of". A provider whose
	// visibility rule is membership (managed resources, whose persona scope grants
	// access to a persona's material) must use this set rather than Persona.
	// Empty when no resolver is wired, which is the same fallback the resources
	// middleware applies.
	Personas []string
	// Roles and IsAdmin are the caller's authority, carried so a provider whose
	// rule admits an administrator can ask (#1584). Only the managed-resource
	// provider reads them, and only on Fetch: a file the caller NAMED is
	// answered on resource.CanAccessResource, which has an administrator arm,
	// while every enumeration here runs on resource.VisibleScopes, which is
	// membership and consults neither field. Adding them therefore widens what
	// a stated reference resolves to and nothing about what a search returns.
	//
	// They are the same two values every other claims derivation in the
	// platform is built from (resource.BuildClaims), so "is this caller an
	// administrator?" cannot come to mean one thing here and another at the
	// REST routes.
	Roles   []string
	IsAdmin bool
	// OnBehalfOf is the address of the person an unattended caller acts for,
	// carried from PlatformContext.OnBehalfOfEmail. A managed-script run
	// authenticates as script:<name>, a principal that is in nobody's library,
	// so discovery scoped on the principal alone would hide the very material
	// its author can see -- and hide the file the same run just wrote, which is
	// filed under the person it acts for (#1419, #1487). Empty for every human
	// caller.
	//
	// It is not a second owner key for every provider: only the managed-resource
	// provider reads it today, through resource.Claims, because the resource
	// scope model is the one that keys a personal library by an identifier a run
	// does not have. A provider whose owner key is the caller's own id or address
	// must keep using those.
	OnBehalfOf string

	// ProducerID is the id of the managed script an unattended caller is a run
	// of, carried from the producer its own writes are recorded under
	// (producedby, content_producers). It is what an asset enumeration scopes
	// such a caller by.
	//
	// It is a second identifier rather than a reuse of UserID because the
	// principal UserID carries is script:<name>, and idx_scripts_name_owner
	// makes a name unique only within its OWNER: two people who each keep a
	// daily-sales present the same principal, so an enumeration scoped on it
	// returns the other person's outputs (#1579). A producer id is the script's
	// own uuid, which is unique and survives both a rename and a transfer.
	//
	// Empty for every human caller, and empty for a run in a deployment that
	// records no producers, which is scoped by nothing and answered with
	// nothing rather than with somebody else's rows.
	ProducerID string

	// SessionID is the unit of work the request belongs to, not part of the
	// identity the per-user providers scope on. The call catalog uses it and
	// nothing else does: reuse of a recorded call is credited to the session
	// that found the record and then ran what it holds, so a fetch has to be
	// attributable to a session to count for anything (#1321). Empty on a
	// caller with no session, which simply records no sighting.
	SessionID string

	// conn is the persona connection boundary for this arm, set by the Router
	// (never by an external caller, hence unexported): it answers whether the
	// caller may see material belonging to a given connection and accumulates the
	// count of candidates that boundary removed. Nil leaves discovery unfiltered,
	// which is what a deployment with no connection scope wired gets.
	conn *connGate
}

// Anonymous reports whether the caller carries no identity at all. The Router
// skips every per-user provider for an anonymous caller.
func (c Caller) Anonymous() bool {
	return c.UserID == "" && c.Email == ""
}

// Query is one knowledge search. It carries two complementary ways to match,
// and a provider uses whichever it supports:
//
//   - Intent is natural-language text matched by relevance. Embedding is the
//     query vector the Router computes once from Intent and shares across
//     providers; nil selects lexical-only ranking.
//   - EntityURNs is an exact, entity-keyed lookup: return knowledge linked to
//     these DataHub URNs (memory uses this, optionally expanded along lineage).
//
// At least one of Intent or EntityURNs is set. Status optionally filters by
// lifecycle/review state where a provider tracks one (insight review status).
// Caller carries the identity per-user providers scope on. Limit caps the
// candidate list each provider returns before the allocator builds the balanced
// display set.
//
// Sources optionally narrows the federation to a subset of provider names
// (e.g. ["datahub"]). It only narrows: an empty Sources queries every provider
// the caller can access, and a name in Sources never opts a caller into a
// provider their scope would otherwise exclude.
type Query struct {
	Intent     string
	Embedding  []float32
	EntityURNs []string
	Status     string
	Caller     Caller
	Limit      int
	Sources    []string
}

// Hit is one knowledge record matched by a provider. Score is the provider's
// own relevance score; the Router normalizes it across providers before fusing,
// so callers see a fused rank, not the raw provider score. Source is the
// provider name, surfaced as provenance. Ref is the record's stable identifier
// within its source (memory id, insight id, asset id) so a caller can fetch the
// full record.
//
// The optional fields carry what the specialized search tools returned, so
// folding them into one search loses nothing: Status is a review or
// lifecycle state (insight pending/approved/...), EntityURNs are the linked
// catalog entities (provenance), and Dimension is the memory dimension or
// category. They are omitted when a source does not populate them.
//
// Temporal validity (valid_from/valid_until) and a live-vs-captured freshness
// flag remain deferred until a provider populates them (the wiki carries season
// windows); adding them now would be unexercised fields.
type Hit struct {
	Text       string   `json:"text"`
	Source     string   `json:"source"`
	Ref        string   `json:"ref"`
	Score      float64  `json:"score"`
	Status     string   `json:"status,omitempty"`
	EntityURNs []string `json:"entity_urns,omitempty"`
	Dimension  string   `json:"dimension,omitempty"`
	// CapturedBy is the author (email) of an authored result such as an insight,
	// so a reviewer can see who recorded it. Omitted for sources with no single
	// author (catalog datasets, API endpoints, connections).
	CapturedBy string `json:"captured_by,omitempty"`
	// Reference is the canonical citation string for the hit's entity
	// (mcp:<type>:<key> for an internal entity, urn:... for DataHub), so an agent
	// can reference it from a knowledge page without hand-assembling or guessing
	// the form. Omitted when the entity is not referenceable.
	Reference string `json:"reference,omitempty"`
	// Verifiable names the queryable table a checkable hit could be settled
	// against, when the source's linked entity resolves through a query provider
	// (#1220). A delivered claim otherwise reads as something to take on trust;
	// this says the claim's subject is one query away. Nil whenever nothing
	// resolved, so a deployment with no query provider serves the payload it
	// always did.
	Verifiable *query.Verifiable `json:"verifiable,omitempty"`
	// Link is set by sources whose hit is backed by a file the MCP client can
	// attach directly (a managed resource). The search surface renders it as an
	// mcp.ResourceLink content block alongside the JSON result, so a client with
	// native resource support can hand the user the file itself instead of only a
	// pointer the model has to dereference. Nil for sources with no file behind
	// the hit.
	Link *HitLink `json:"link,omitempty"`
	// Table names the query-engine table a registered file is readable as
	// (#1327). A search hit for an uploaded CSV otherwise says only that the
	// file exists; this says it can be joined, and to what.
	//
	// A hit carries one table because it points at somewhere the data can be
	// queried rather than inventorying every registration -- fetch returns
	// the inventory (#1627). It is the newest registration whose follow has
	// not failed. Nil for a source with no file behind it, a file nobody
	// registered, and one whose every registration carries a follow error.
	Table *HitTable `json:"table,omitempty"`
}

// HitTable is one registration over a file: the connection to run against, the
// name to write in the FROM clause, and the state of the registration itself.
//
// Sample carries a statement showing the CAST a join needs, because a table
// registered over a CSV has VARCHAR for every column and the obvious join
// fails with a type error that explains nothing. Stale says the file has moved
// on since the table was registered, so the rows are the revision that was
// current then; correct SQL over stale bytes is the failure nothing else
// surfaces.
//
// RegistrationID, Follow, Repair and FollowError are the four facts
// manage_table action=list reports, so the two surfaces cannot disagree about
// one registration (#1627). FollowError decides whether the table can be
// queried at all: a follow that could not move the registration leaves its
// reason here, and the table it names may be gone.
type HitTable struct {
	RegistrationID string `json:"registration_id,omitempty"`
	Connection     string `json:"connection"`
	Table          string `json:"query_table"`
	Sample         string `json:"sample_sql,omitempty"`
	Stale          bool   `json:"stale,omitempty"`
	Follow         bool   `json:"follow"`
	Repair         bool   `json:"repair"`
	FollowError    string `json:"follow_error,omitempty"`
}

// HitLink is the client-attachable file behind a Hit: the canonical resource URI
// plus the labels an MCP resource link carries. It is transport-neutral (no MCP
// SDK types) so providers stay free of the protocol layer; the search surface
// converts it.
type HitLink struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
}

// Provider is one searchable knowledge store behind the Router. Name is the
// provenance label stamped on every Hit. Scope drives the Router's access
// rules. Search returns the provider's own ranked hits for the query; the
// Router owns cross-provider normalization and fusion, so a provider only needs
// to rank within itself.
type Provider interface {
	Name() string
	Scope() Scope
	Search(ctx context.Context, q Query) ([]Hit, error)
}

// mergeArms runs a two-arm provider search and merges the results: the entity-keyed
// arm first (exact, entity-matched), then the text-relevance arm, de-duplicated
// across both via a shared seen-set so a result found both ways appears once at the
// entity arm's rank. It holds the dedup-and-merge contract once for the catalog-style
// sources (datahub, documents, knowledge pages) that share this shape, so the policy
// cannot drift between hand-copied Search methods. A text-arm error blanks the
// provider (consistent with every two-arm provider); the entity arm isolates per-item
// errors internally.
func mergeArms(
	ctx context.Context, q Query,
	entityArm func(context.Context, Query, map[string]bool) []Hit,
	textArm func(context.Context, Query, map[string]bool) ([]Hit, error),
) ([]Hit, error) {
	seen := make(map[string]bool)
	entityHits := entityArm(ctx, q, seen)
	textHits, err := textArm(ctx, q, seen)
	if err != nil {
		return nil, err
	}
	if len(entityHits) == 0 && len(textHits) == 0 {
		return nil, nil
	}
	return append(entityHits, textHits...), nil
}
