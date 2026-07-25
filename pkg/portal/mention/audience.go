package mention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// Thread target types a mention audience can be resolved for. They mirror the
// discriminators the thread substrate stores on a row.
const (
	TargetAsset         = "asset"
	TargetCollection    = "collection"
	TargetPrompt        = "prompt"
	TargetKnowledgePage = "knowledge_page"
	TargetStandalone    = "standalone"
)

// ErrUnknownTarget is returned for a target type the audience rule does not
// cover, so an unrecognized target fails closed rather than resolving to
// everyone.
var ErrUnknownTarget = errors.New("mention: unknown thread target type")

// defaultListLimit and maxListLimit bound a candidate page.
const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// Target names the thread target whose audience is being resolved. ID is empty
// for the standalone channel.
type Target struct {
	Type string
	ID   string
}

// Person is one member of a target's audience. Names come from the known-users
// directory and are empty for an address that has been shared with but has
// never authenticated.
type Person struct {
	Email     string `json:"email" example:"marcus.johnson@example.com"`
	FirstName string `json:"first_name,omitempty" example:"Marcus"`
	LastName  string `json:"last_name,omitempty" example:"Johnson"`
	// Confirmed reports whether this person has ever authenticated. A share
	// can name someone who never signed in; the picker shows them, so the
	// author knows a mention will reach an inbox before it reaches a session.
	Confirmed bool `json:"confirmed" example:"true"`
}

// ListOptions filters a candidate page.
type ListOptions struct {
	// Query matches case-insensitively against the address and the directory
	// name; empty matches everyone in the audience.
	Query string
	// Exclude is the caller's own address, which is never offered: mentioning
	// yourself notifies nobody (the enqueue path drops self-notification).
	Exclude string
	// Limit caps the page, defaulting to defaultListLimit.
	Limit int
}

// Audience answers who may be mentioned on a thread target.
//
// The rule mirrors the portal's own view checks so a mentionable person is
// always a person who can open the thing being discussed:
//
//   - asset: the owner, recipients of an active direct share, and recipients
//     of an active share on a collection holding the asset (portal.userCanViewAsset)
//   - collection: the owner and recipients of an active share
//   - prompt: everyone for a persona- or global-scoped prompt, since those are
//     visible platform-wide; the owner and share recipients for a personal one
//     (portal.userCanViewPrompt)
//   - knowledge page and the standalone channel: everyone, since both are open
//     to any authenticated user (portal.threadKnowledgePageAccess)
//
// "Everyone" means the known-users directory rather than any address that
// parses: a comment must not be able to send mail to an arbitrary recipient.
//
// Administrators can view every target but are not enumerable from the
// database (roles arrive on the caller's token, not in a table), so an admin
// appears in an audience only through ownership or a share. A mention of an
// admin outside the audience is treated like any other non-member: kept as
// text, notifying nobody.
type Audience struct {
	db *sql.DB
}

// NewAudience builds an audience resolver over the platform database.
func NewAudience(db *sql.DB) *Audience {
	return &Audience{db: db}
}

// List returns the audience members matching opts, ordered by address. It is
// the type-ahead behind the composer's mention picker.
func (a *Audience) List(ctx context.Context, t Target, opts ListOptions) ([]Person, error) {
	source, args, err := a.source(ctx, t)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	match := "%" + escapeLike(normalize(opts.Query)) + "%"
	exclude := normalize(opts.Exclude)

	query, args := listQuery(source, args, match, exclude, limit)
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing mention candidates: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	people := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.Email, &p.FirstName, &p.LastName, &p.Confirmed); err != nil {
			return nil, fmt.Errorf("scanning mention candidate: %w", err)
		}
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating mention candidates: %w", err)
	}
	return people, nil
}

// Eligible returns the subset of emails that belong to the target's audience,
// preserving the caller's order. It is the write path's filter: an address it
// drops stays plain text in the comment body and notifies nobody.
func (a *Audience) Eligible(ctx context.Context, t Target, emails []string) ([]string, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	source, args, err := a.source(ctx, t)
	if err != nil {
		return nil, err
	}
	lowered := make([]string, 0, len(emails))
	for _, e := range emails {
		lowered = append(lowered, normalize(e))
	}

	// #nosec G202 -- source is one of this package's SQL constants (never caller
	// input) and the only interpolated value is the placeholder index; the
	// addresses themselves are bound as a parameter.
	query := "SELECT email FROM (" + source + ") AS audience WHERE email = ANY($" +
		fmt.Sprint(len(args)+1) + ")"
	rows, err := a.db.QueryContext(ctx, query, append(args, pq.Array(lowered))...)
	if err != nil {
		return nil, fmt.Errorf("filtering mentions to the audience: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	member := make(map[string]struct{}, len(lowered))
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("scanning audience member: %w", err)
		}
		member[email] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating audience members: %w", err)
	}

	var out []string
	for _, e := range lowered {
		if _, ok := member[e]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// listQuery wraps an audience source in the directory join, name/address
// filter, self-exclusion, and page limit. source produces one lower-cased
// email column; args are its parameters, so the filter parameters continue the
// numbering after them.
func listQuery(source string, args []any, match, exclude string, limit int) (query string, params []any) {
	n := len(args)
	query = `
		SELECT audience.email,
		       COALESCE(u.first_name, '') AS first_name,
		       COALESCE(u.last_name, '')  AS last_name,
		       COALESCE(u.confirmed, FALSE) AS confirmed
		  FROM (` + source + `) AS audience
		  LEFT JOIN users u ON u.email = audience.email
		 WHERE (audience.email LIKE $` + fmt.Sprint(n+1) + ` ESCAPE '` + likeEscape + `'
		        OR LOWER(COALESCE(u.first_name, '') || ' ' || COALESCE(u.last_name, ''))
		           LIKE $` + fmt.Sprint(n+1) + ` ESCAPE '` + likeEscape + `')
		   AND audience.email <> $` + fmt.Sprint(n+2) + `
		 ORDER BY audience.email
		 LIMIT $` + fmt.Sprint(n+3)
	return query, append(args, match, exclude, limit)
}

// likeEscape is the escape character the audience filter binds its patterns
// with, so a "%" or "_" typed into the picker matches itself.
const likeEscape = `\`

// likePattern escapes the LIKE metacharacters in a user-typed query. Without
// it, "first_last" matches any character where the underscore is and a lone "%"
// lists the whole audience -- offering people the author never searched for, to
// a picker whose whole job is choosing who receives an email.
var likePattern = strings.NewReplacer(likeEscape, likeEscape+likeEscape,
	"%", likeEscape+"%", "_", likeEscape+"_")

// escapeLike returns q with its LIKE metacharacters neutralized.
func escapeLike(q string) string { return likePattern.Replace(q) }

// source returns the SQL producing the target's audience as a single
// lower-cased email column, together with its parameters. Open targets resolve
// to the whole known-users directory.
func (a *Audience) source(ctx context.Context, t Target) (query string, args []any, err error) {
	switch t.Type {
	case TargetAsset, TargetCollection:
		q, params := grantSource(t)
		return q, params, nil
	case TargetPrompt:
		return a.promptSource(ctx, t.ID)
	case TargetKnowledgePage, TargetStandalone:
		return directoryAudienceSQL, nil, nil
	default:
		return "", nil, fmt.Errorf("target type %q: %w", t.Type, ErrUnknownTarget)
	}
}

// Grantees returns the addresses holding an explicit grant on a target: its
// owner and the recipients of active shares (for an asset, including shares of
// a collection that holds it). It never widens to the directory the way the
// mention audience does for an open target, because it answers a different
// question -- who is attached to this item and should hear about activity on
// it, not who is allowed to read it. Targets with no grant concept (knowledge
// pages, the standalone channel) return nothing.
func (a *Audience) Grantees(ctx context.Context, targetType, targetID string) ([]string, error) {
	source, args := grantSource(Target{Type: targetType, ID: targetID})
	if source == "" {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx, source, args...)
	if err != nil {
		return nil, fmt.Errorf("listing target grantees: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup after read-only query

	var out []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("scanning target grantee: %w", err)
		}
		out = append(out, email)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating target grantees: %w", err)
	}
	return out, nil
}

// grantSource returns the SQL for a target's explicit grants (owner plus active
// share recipients) and its parameters, or "" for a target that has none.
func grantSource(t Target) (query string, args []any) {
	switch t.Type {
	case TargetAsset:
		return assetAudienceSQL, []any{t.ID}
	case TargetCollection:
		return collectionAudienceSQL, []any{t.ID}
	case TargetPrompt:
		return promptAudienceSQL, []any{t.ID}
	default:
		return "", nil
	}
}

// promptSource picks the mention audience for a prompt from its scope: a
// personal prompt is owner-and-shares, anything else is visible platform-wide
// (portal.userCanViewPrompt admits every caller for a persona- or global-scoped
// prompt). A prompt that no longer exists falls back to the grant source, which
// matches no rows.
func (a *Audience) promptSource(ctx context.Context, id string) (query string, args []any, err error) {
	target := Target{Type: TargetPrompt, ID: id}
	var scope string
	scopeErr := a.db.QueryRowContext(ctx, `SELECT scope FROM prompts WHERE id = $1`, id).Scan(&scope)
	switch {
	case errors.Is(scopeErr, sql.ErrNoRows), scopeErr == nil && scope == scopePersonal:
		q, params := grantSource(target)
		return q, params, nil
	case scopeErr != nil:
		return "", nil, fmt.Errorf("reading prompt scope: %w", scopeErr)
	default:
		return directoryAudienceSQL, nil, nil
	}
}

// scopePersonal is the prompt scope whose visibility is owner-and-shares. It
// is spelled here rather than imported so this package stays independent of
// the prompt domain.
const scopePersonal = "personal"

// activeShare is the predicate for a share that still grants access AND names
// its recipient by address.
//
// A share carrying only shared_with_user_id grants access but records no
// address anywhere in the platform (the users directory is keyed by email, with
// no user-id column), so such a person cannot be mailed and cannot be offered
// in a picker that inserts an address. They are outside the audience by
// construction rather than by policy; every share the portal itself writes --
// the share dialog and the public-link auto-promote (pkg/portal/public.go) --
// carries the address.
const activeShare = `revoked = FALSE
		     AND (expires_at IS NULL OR expires_at > NOW())
		     AND COALESCE(shared_with_email, '') <> ''`

// assetAudienceSQL is the owner plus recipients of active direct shares and of
// active shares on any collection holding the asset. Every branch is gated on
// the asset still existing: a share row outlives the soft-delete of its asset,
// so without the guard a deleted asset would keep an audience.
const assetAudienceSQL = `
	SELECT LOWER(owner_email) AS email
	  FROM portal_assets
	 WHERE id = $1 AND owner_email <> '' AND deleted_at IS NULL
	 UNION
	SELECT LOWER(shared_with_email)
	  FROM portal_shares
	 WHERE asset_id = $1 AND ` + assetLives + ` AND ` + activeShare + `
	 UNION
	SELECT LOWER(ps.shared_with_email)
	  FROM portal_shares ps
	  JOIN portal_collection_sections cs ON cs.collection_id = ps.collection_id
	  JOIN portal_collection_items ci ON ci.section_id = cs.id
	 WHERE ci.asset_id = $1
	   AND ` + assetLives + `
	   AND ps.collection_id IS NOT NULL
	   AND ps.revoked = FALSE
	   AND (ps.expires_at IS NULL OR ps.expires_at > NOW())
	   AND COALESCE(ps.shared_with_email, '') <> ''`

// assetLives requires the target asset to still exist, for the share-derived
// branches that do not otherwise touch portal_assets.
const assetLives = `EXISTS (SELECT 1 FROM portal_assets a WHERE a.id = $1 AND a.deleted_at IS NULL)`

// collectionLives is assetLives for a collection target.
const collectionLives = `EXISTS (SELECT 1 FROM portal_collections c WHERE c.id = $1 AND c.deleted_at IS NULL)`

// collectionAudienceSQL is the owner plus recipients of active shares.
const collectionAudienceSQL = `
	SELECT LOWER(owner_email) AS email
	  FROM portal_collections
	 WHERE id = $1 AND owner_email <> '' AND deleted_at IS NULL
	 UNION
	SELECT LOWER(shared_with_email)
	  FROM portal_shares
	 WHERE collection_id = $1 AND ` + collectionLives + ` AND ` + activeShare

// promptAudienceSQL is a personal prompt's owner plus recipients of active
// shares.
const promptAudienceSQL = `
	SELECT LOWER(owner_email) AS email
	  FROM prompts
	 WHERE id = $1 AND owner_email <> ''
	 UNION
	SELECT LOWER(shared_with_email)
	  FROM portal_shares
	 WHERE prompt_id = $1 AND ` + activeShare

// directoryAudienceSQL is every person the platform knows: the audience of a
// target open to any authenticated user.
const directoryAudienceSQL = `SELECT LOWER(email) AS email FROM users`
