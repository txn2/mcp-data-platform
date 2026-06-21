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
// knowledge_search agent tool (pull), and later as a retriever wired into the
// enrichment middleware (push). PR1 (#632) builds the pull path with the
// memory, insights, and assets providers; the technical catalog (datahub) and
// prompt providers, and push injection, land in follow-up PRs.
package knowledge

import "context"

// Scope declares whether a provider's records are visible to every caller or
// only to the caller who owns them. The Router uses it to decide which
// providers a request may touch and with what identity.
type Scope int

const (
	// ScopeShared marks a provider whose records are visible to all callers
	// (the technical catalog, reviewed-and-shared knowledge). A shared
	// provider is queried for every request, with or without a caller
	// identity.
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
}

// Anonymous reports whether the caller carries no identity at all. The Router
// skips every per-user provider for an anonymous caller.
func (c Caller) Anonymous() bool {
	return c.UserID == "" && c.Email == ""
}

// Query is one knowledge search. Intent is the natural-language text to match.
// Embedding is the query vector the Router computes once and shares across
// providers; it is nil when no embedder is configured, which selects
// lexical-only ranking. Caller carries the identity per-user providers scope
// on. Limit caps results per provider before fusion.
type Query struct {
	Intent    string
	Embedding []float32
	Caller    Caller
	Limit     int
}

// Hit is one knowledge record matched by a provider. Score is the provider's
// own relevance score; the Router normalizes it across providers before fusing,
// so callers see a fused rank, not the raw provider score. Source is the
// provider name, surfaced as provenance. Ref is the record's stable identifier
// within its source (memory id, insight id, asset id) so a caller can fetch the
// full record.
//
// The Hit shape is intentionally minimal in PR1. Temporal validity
// (valid_from/valid_until) and a live-vs-captured freshness flag are part of
// the eventual contract (#632) but are deferred until a provider populates
// them (the technical catalog is "live", the wiki carries season windows);
// adding them now would be unexercised fields. See the #632 implementation log.
type Hit struct {
	Text   string  `json:"text"`
	Source string  `json:"source"`
	Ref    string  `json:"ref"`
	Score  float64 `json:"score"`
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
