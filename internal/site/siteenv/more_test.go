package siteenv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// withFiles files release n of the site holding files, served by env.
func withFiles(store *memStore, n int, env string, files ...site.File) {
	released(store, n, env)
	store.releases[siteID][0].Files = files
	store.releases[siteID][0].Redirects = []site.Redirect{{From: "/old", To: "/new", Status: 301}}
}

// TestContentOf pins #2629's reading rule: dev's file, a release's file for
// the people who manage the site and for someone an environment serving it is
// shared with, the rendered _redirects, and the refusals.
func TestContentOf(t *testing.T) {
	ops, store, _ := fixture(t)
	logo := site.File{Path: "img/logo.png", SHA256: "abc", ContentType: "image/png", Size: 3}
	withFiles(store, 1, "stage", logo)
	withFiles(store, 2, site.EnvDev, logo, site.File{Path: "img/logo.png.bak", SHA256: "def", Size: 1})
	ctx := context.Background()

	got, err := ops.ContentOf(ctx, owner, "marketing", "img/logo.png", 0)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Release)
	assert.Equal(t, "image/png", got.ContentType)
	assert.Equal(t, sitesvc.ObjectKey(siteID, logo, ""), got.Key)

	got, err = ops.ContentOf(ctx, owner, "marketing", "img/logo.png", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Release)
	got, err = ops.ContentOf(ctx, owner, "marketing", "_redirects", 1)
	require.NoError(t, err)
	assert.Contains(t, string(got.Text), "/old")
	got, err = ops.ContentOf(ctx, owner, "marketing", "_headers", 1)
	require.NoError(t, err)
	assert.NotNil(t, got.Text)

	var r *sitesvc.RefusedError
	_, err = ops.ContentOf(ctx, owner, "marketing", "../x", 0)
	require.ErrorAs(t, err, &r)
	_, err = ops.ContentOf(ctx, owner, "marketing", "missing.png", 1)
	require.ErrorAs(t, err, &r)
	_, err = ops.ContentOf(ctx, stranger, "marketing", "img/logo.png", 1)
	require.ErrorIs(t, err, site.ErrNotAllowed)
	_, err = ops.ContentOf(ctx, owner, "nope", "img/logo.png", 1)
	require.ErrorIs(t, err, site.ErrNotFound)

	store.grants[siteID] = map[string]site.Grant{viewer.Email: {Email: viewer.Email, Level: site.LevelView, Environments: []string{"stage"}}}
	_, err = ops.ContentOf(ctx, viewer, "marketing", "img/logo.png", 1)
	require.NoError(t, err, "someone stage is shared with reads the release stage serves")
	_, err = ops.ContentOf(ctx, viewer, "marketing", "img/logo.png", 2)
	require.ErrorIs(t, err, site.ErrNotAllowed, "and not one no environment they open serves")

	store.releases[siteID][1].Pruned = true
	_, err = ops.ContentOf(ctx, owner, "marketing", "img/logo.png", 1)
	require.ErrorAs(t, err, &r, "a pruned release has no files to read")
}

// TestCheckName pins #2636's server rule.
func TestCheckName(t *testing.T) {
	ops, _, _ := fixture(t)
	ops.Config = siteconfig.Config{PagesDomain: "pages.example.net"}
	ops.Apps = fakeApps{"shop": &application.Application{ID: "app_shop", Name: "shop"}}
	ctx := context.Background()
	got, err := ops.CheckName(ctx, "My Website")
	require.NoError(t, err)
	assert.Equal(t, "my-website", got.Name)
	assert.Empty(t, got.Refusal)
	assert.Equal(t, "https://my-website--dev.pages.example.net", got.Addresses[site.EnvDev])
	assert.Equal(t, "https://my-website.pages.example.net", got.Addresses[site.EnvProd])
	assert.Equal(t, 32, got.EnvironmentNameRoom)
	for typed, want := range map[string]string{
		"!!!":                   "at least one letter",
		"Marketing":             "exists",
		"Shop":                  "name of an application",
		strings.Repeat("a", 58): "at most 57",
		"ab":                    "two characters",
	} {
		got, err := ops.CheckName(ctx, typed)
		require.NoError(t, err)
		assert.Contains(t, got.Refusal, want, typed)
		// A person reads it in the New site dialog: a sentence.
		assert.Regexp(t, `^[A-Z"].*\.$`, got.Refusal, typed)
	}
}

// TestAgentPublicDomain pins #2643 for making a domain public: an agent's
// waits on the person, is carried out once they accepted it, and a person's
// own is unchanged.
func TestAgentPublicDomain(t *testing.T) {
	ops, store, approvals := fixture(t)
	ops.Config = siteconfig.Config{PagesDomain: "pages.example.net"}
	ctx := context.Background()
	agent := owner
	agent.Agent = true

	_, err := ops.AddDomain(ctx, agent, sitesvc.DomainRequest{Hostname: "www.client.example", Site: "marketing", Environment: "stage", Public: true})
	wait, ok := IsPersonDecision(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, MovePublic, wait.Move.Kind)
	require.NotNil(t, store.domains["www.client.example"], "the domain is added private")
	assert.False(t, store.domains["www.client.example"].Public)

	agent.Decided = wait.Move.Key()
	got, err := ops.AddDomain(ctx, agent, sitesvc.DomainRequest{Hostname: "www.client.example", Site: "marketing", Environment: "stage", Public: true})
	require.NoError(t, err, "asking again once the person accepted makes the domain it added public")
	assert.Equal(t, site.DecidedByElicitation, got.DecidedBy)
	assert.True(t, store.domains["www.client.example"].Public)

	got, err = ops.AddDomain(ctx, owner, sitesvc.DomainRequest{Hostname: "beta.client.example", Site: "marketing", Environment: "stage", Public: true})
	require.NoError(t, err)
	assert.Empty(t, got.DecidedBy, "a person's own domain is theirs")

	_, err = ops.SetDomainAccess(ctx, sitesvc.Actor{Email: owner.Email, Agent: true}, "beta.client.example", false, "")
	require.NoError(t, err, "making a domain private never waits")
	approvals.envApproved = true
	got, err = ops.SetDomainAccess(ctx, sitesvc.Actor{Email: owner.Email, Agent: true}, "beta.client.example", true, "")
	require.NoError(t, err)
	assert.Equal(t, site.DecidedByApproval, got.DecidedBy)

	req, err := ops.RequestMove(ctx, agent, wait.Move)
	require.NoError(t, err)
	assert.Equal(t, "apr_env", req.ID)
	assert.Nil(t, wrapDomain(nil, ""))
}

// TestAgentRollback: an agent's rollback of an environment people use waits
// on the person; a stranger's gets the service's own refusal, asking no one.
func TestAgentRollback(t *testing.T) {
	ops, store, approvals := fixture(t)
	released(store, 1, "stage")
	released(store, 2, "stage")
	ctx := context.Background()
	agent := owner
	agent.Agent = true
	_, err := ops.Rollback(ctx, agent, "marketing", "stage", 0)
	wait, ok := IsPersonDecision(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, MoveRollback, wait.Move.Kind)
	assert.Contains(t, wait.Move.Question(), "roll stage of site marketing back to release 1")

	_, err = ops.Rollback(ctx, sitesvc.Actor{Email: stranger.Email, Agent: true}, "marketing", "stage", 0)
	require.ErrorIs(t, err, site.ErrNotAllowed)
	_, ok = IsPersonDecision(err)
	assert.False(t, ok, "nothing about the site reaches someone who cannot change it")

	approvals.approved = true
	got, err := ops.Rollback(ctx, agent, "marketing", "stage", 0)
	require.NoError(t, err)
	assert.Equal(t, site.DecidedByApproval, got.DecidedBy)

	ops.Approvals = nil
	_, err = ops.RequestMove(ctx, agent, wait.Move)
	require.Error(t, err)
	assert.False(t, ops.moveApproved(ctx, wait.Move))
}

func TestQuestionNamesTheFiles(t *testing.T) {
	m := AgentMove{
		Kind: MovePromote, Site: "marketing", Target: "prod", Release: 4, Current: 3, DevAddress: "https://marketing--dev.example",
		Changed: &sitesvc.Comparison{Changed: []string{"a", "b", "c", "d", "e", "f"}, Added: []string{"g", "h", "i", "j", "k"}},
	}
	q := m.Question()
	assert.Contains(t, q, "promote release 4 of site marketing to prod, where it replaces release 3")
	assert.Contains(t, q, "https://marketing--dev.example")
	assert.Contains(t, q, "and 1 more")
	assert.Contains(t, (&PersonDecisionError{Move: m}).Error(), "APPROVAL_REQUIRED")
	assert.NotEqual(t, m.Key(), AgentMove{Kind: MovePromote, Release: 5}.Key())
	assert.Empty(t, firstNames(&sitesvc.Comparison{}, 3))
	_, ok := IsPersonDecision(errors.New("other"))
	assert.False(t, ok)
}

func TestInUse(t *testing.T) {
	err := inUse(errors.New("plain"))
	assert.EqualError(t, err, "plain")
}
