//go:build integration

package mention

// Real-Postgres tests for the @-mention audience (#627). They exercise what
// sqlmock cannot: that the audience SQL is valid against the real schema, that
// the union across owner, direct shares, and collection shares actually resolves
// the people the portal's own view check would admit, and that the jsonb
// containment the mentions inbox queries matches what the write path stores.
// Run under `make test-realdb`.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const (
	ownerEmail    = "owner@example.com"
	sharedEmail   = "shared@example.com"
	viaCollection = "collection.viewer@example.com"
	strangerEmail = "stranger@example.com"
)

// seedPeople puts the cast in the known-users directory, which is where the
// picker reads names and which bounds who an open target can mention.
func seedPeople(t *testing.T, db *sql.DB, emails ...string) {
	t.Helper()
	for _, email := range emails {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO users (email, first_name, last_name, confirmed)
			 VALUES ($1, 'First', 'Last', TRUE) ON CONFLICT (email) DO NOTHING`, email)
		require.NoError(t, err)
	}
}

// seedAssetWithShares creates an asset owned by ownerEmail, shared directly with
// sharedEmail, and held by a collection shared with viaCollection.
func seedAssetWithShares(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}

	exec(`INSERT INTO portal_assets (id, owner_id, owner_email, name, content_type, s3_bucket, s3_key)
	      VALUES ('asset_1', 'u_owner', $1, 'Report', 'text/markdown', 'b', 'k')`, ownerEmail)
	exec(`INSERT INTO portal_collections (id, owner_id, owner_email, name)
	      VALUES ('col_1', 'u_owner', $1, 'Quarterly')`, ownerEmail)
	exec(`INSERT INTO portal_collection_sections (id, collection_id, title, position)
	      VALUES ('sec_1', 'col_1', 'Main', 0)`)
	exec(`INSERT INTO portal_collection_items (id, section_id, asset_id, position)
	      VALUES ('itm_1', 'sec_1', 'asset_1', 0)`)
	exec(`INSERT INTO portal_shares (id, asset_id, token, created_by, shared_with_email, permission)
	      VALUES ('shr_direct', 'asset_1', 'tok_direct', $1, $2, 'viewer')`, ownerEmail, sharedEmail)
	exec(`INSERT INTO portal_shares (id, collection_id, token, created_by, shared_with_email, permission)
	      VALUES ('shr_coll', 'col_1', 'tok_coll', $1, $2, 'viewer')`, ownerEmail, viaCollection)
	// Revoked and expired shares must not admit anyone.
	exec(`INSERT INTO portal_shares (id, asset_id, token, created_by, shared_with_email, permission, revoked)
	      VALUES ('shr_revoked', 'asset_1', 'tok_revoked', $1, 'revoked@example.com', 'viewer', TRUE)`, ownerEmail)
	exec(`INSERT INTO portal_shares (id, asset_id, token, created_by, shared_with_email, permission, expires_at)
	      VALUES ('shr_expired', 'asset_1', 'tok_expired', $1, 'expired@example.com', 'viewer', NOW() - INTERVAL '1 day')`,
		ownerEmail)
}

func TestRealDB_AssetAudience(t *testing.T) {
	db := testdb.New(t)
	seedPeople(t, db, ownerEmail, sharedEmail, viaCollection, strangerEmail,
		"revoked@example.com", "expired@example.com")
	seedAssetWithShares(t, db)
	audience := NewAudience(db)
	target := Target{Type: TargetAsset, ID: "asset_1"}

	people, err := audience.List(context.Background(), target, ListOptions{})
	require.NoError(t, err)
	emails := make([]string, 0, len(people))
	for _, p := range people {
		emails = append(emails, p.Email)
	}
	assert.ElementsMatch(t, []string{ownerEmail, sharedEmail, viaCollection}, emails,
		"the audience is the owner, direct share recipients, and collection-share recipients")

	eligible, err := audience.Eligible(context.Background(), target,
		[]string{sharedEmail, strangerEmail, "revoked@example.com", "expired@example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{sharedEmail}, eligible,
		"a stranger, a revoked share, and an expired share are all outside the audience")
}

func TestRealDB_AudienceListFiltersAndNames(t *testing.T) {
	db := testdb.New(t)
	seedPeople(t, db, ownerEmail, sharedEmail, viaCollection)
	seedAssetWithShares(t, db)
	audience := NewAudience(db)

	people, err := audience.List(context.Background(), Target{Type: TargetAsset, ID: "asset_1"},
		ListOptions{Query: "shared", Exclude: ownerEmail})
	require.NoError(t, err)
	require.Len(t, people, 1)
	assert.Equal(t, sharedEmail, people[0].Email)
	assert.Equal(t, "First", people[0].FirstName, "names come from the directory join")
	assert.True(t, people[0].Confirmed)
}

func TestRealDB_Grantees(t *testing.T) {
	db := testdb.New(t)
	seedPeople(t, db, ownerEmail, sharedEmail, viaCollection)
	seedAssetWithShares(t, db)
	audience := NewAudience(db)

	grantees, err := audience.Grantees(context.Background(), TargetAsset, "asset_1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{ownerEmail, sharedEmail, viaCollection}, grantees)

	// An open target has no grantees, so a comment on a knowledge page never
	// fans out to the whole directory.
	pageGrantees, err := audience.Grantees(context.Background(), TargetKnowledgePage, "kp_1")
	require.NoError(t, err)
	assert.Empty(t, pageGrantees)
}

func TestRealDB_PromptAudienceFollowsScope(t *testing.T) {
	db := testdb.New(t)
	seedPeople(t, db, ownerEmail, strangerEmail)
	ctx := context.Background()
	// The prompts table keys on a uuid, so the ids are read back rather than
	// invented.
	var personalID, globalID string
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO prompts (name, scope, owner_email, content)
		 VALUES ('personal-one', 'personal', $1, 'body') RETURNING id`, ownerEmail).Scan(&personalID))
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO prompts (name, scope, owner_email, content)
		 VALUES ('global-one', 'global', $1, 'body') RETURNING id`, ownerEmail).Scan(&globalID))
	audience := NewAudience(db)

	personal, err := audience.Eligible(ctx, Target{Type: TargetPrompt, ID: personalID},
		[]string{ownerEmail, strangerEmail})
	require.NoError(t, err)
	assert.Equal(t, []string{ownerEmail}, personal, "a personal prompt is owner-and-shares")

	global, err := audience.Eligible(ctx, Target{Type: TargetPrompt, ID: globalID},
		[]string{ownerEmail, strangerEmail})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{ownerEmail, strangerEmail}, global,
		"a global prompt is visible platform-wide, so anyone known may be mentioned on it")

	missing, err := audience.Eligible(ctx,
		Target{Type: TargetPrompt, ID: "00000000-0000-0000-0000-000000000000"}, []string{ownerEmail})
	require.NoError(t, err)
	assert.Empty(t, missing, "a prompt that no longer exists must not resolve to everyone")
}

func TestRealDB_OpenTargetAudienceIsTheDirectory(t *testing.T) {
	db := testdb.New(t)
	seedPeople(t, db, ownerEmail, strangerEmail)
	audience := NewAudience(db)

	for _, targetType := range []string{TargetKnowledgePage, TargetStandalone} {
		eligible, err := audience.Eligible(context.Background(),
			Target{Type: targetType, ID: "x"}, []string{strangerEmail, "nobody@example.com"})
		require.NoError(t, err)
		assert.Equal(t, []string{strangerEmail}, eligible,
			"%s admits any known user, and only known users", targetType)
	}
}

// The mentions inbox finds a thread through jsonb containment against the same
// document the write path stores, over the index migration 000090 creates.
func TestRealDB_MentionContainmentMatchesStoredMetadata(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedPeople(t, db, ownerEmail, sharedEmail)
	seedAssetWithShares(t, db)

	metadata, err := WithMentions(nil, []string{sharedEmail})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO portal_threads (id, kind, target_type, asset_id, author_id, author_email)
		 VALUES ('thr_1', 'comment', 'asset', 'asset_1', 'u_owner', $1)`, ownerEmail)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO portal_thread_events (id, thread_id, event_type, author_id, author_email, body, metadata)
		 VALUES ('evt_1', 'thr_1', 'comment', 'u_owner', $1, 'ping', $2)`, ownerEmail, string(metadata))
	require.NoError(t, err)

	var found int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_thread_events
		  WHERE metadata -> 'mentions' @> $1::jsonb`, ContainmentFilter(sharedEmail)).Scan(&found))
	assert.Equal(t, 1, found, "the inbox filter must match what the write path stored")

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM portal_thread_events
		  WHERE metadata -> 'mentions' @> $1::jsonb`, ContainmentFilter(strangerEmail)).Scan(&found))
	assert.Zero(t, found)

	// The stored document is the shape FromMetadata reads back.
	var stored string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT metadata::text FROM portal_thread_events WHERE id = 'evt_1'`).Scan(&stored))
	assert.Equal(t, []string{sharedEmail}, FromMetadata(json.RawMessage(stored)))
}

// A share row outlives the soft-delete of its asset, so the audience must be
// gated on the asset still existing: a deleted item has nobody to mention.
func TestRealDB_DeletedTargetHasNoAudience(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedPeople(t, db, ownerEmail, sharedEmail, viaCollection)
	seedAssetWithShares(t, db)
	audience := NewAudience(db)
	target := Target{Type: TargetAsset, ID: "asset_1"}

	eligible, err := audience.Eligible(ctx, target, []string{sharedEmail})
	require.NoError(t, err)
	require.Equal(t, []string{sharedEmail}, eligible, "precondition: shared before the delete")

	_, err = db.ExecContext(ctx, `UPDATE portal_assets SET deleted_at = NOW() WHERE id = 'asset_1'`)
	require.NoError(t, err)

	eligible, err = audience.Eligible(ctx, target, []string{ownerEmail, sharedEmail, viaCollection})
	require.NoError(t, err)
	assert.Empty(t, eligible, "a deleted asset has no audience, direct or through a collection")

	grantees, err := audience.Grantees(ctx, TargetAsset, "asset_1")
	require.NoError(t, err)
	assert.Empty(t, grantees, "and nobody to notify about a comment on it")
}

// The picker must not widen on LIKE metacharacters typed into the search box.
func TestRealDB_PickerEscapesWildcards(t *testing.T) {
	db := testdb.New(t)
	seedPeople(t, db, ownerEmail, sharedEmail, viaCollection)
	seedAssetWithShares(t, db)
	audience := NewAudience(db)
	target := Target{Type: TargetAsset, ID: "asset_1"}

	all, err := audience.List(context.Background(), target, ListOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, all)

	wild, err := audience.List(context.Background(), target, ListOptions{Query: "%"})
	require.NoError(t, err)
	assert.Empty(t, wild, "a literal %% matches nobody rather than listing the whole audience")

	underscore, err := audience.List(context.Background(), target, ListOptions{Query: "share_"})
	require.NoError(t, err)
	assert.Empty(t, underscore, "_ is matched literally, not as a single-character wildcard")
}
