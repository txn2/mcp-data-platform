package siteenv

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

const (
	siteID  = "site_1"
	envProd = "prod"
)

var (
	owner    = sitesvc.Actor{Email: "jane@example.com", Persona: "analyst"}
	manager  = sitesvc.Actor{Email: "mo@example.com", Persona: "analyst"}
	viewer   = sitesvc.Actor{Email: "vi@client.example"}
	stranger = sitesvc.Actor{Email: "joe@example.com"}
	admin    = sitesvc.Actor{Email: "admin@example.com", Persona: "admin", Admin: true}
)

// fixture is the operations over one site owned by jane with dev, stage and a
// prod that requires approval.
//
//nolint:revive // a fixture hands back each fake a test drives
func fixture(t *testing.T) (Ops, *memStore, *fakeApprovals) {
	t.Helper()
	store, approvals := newMem(), &fakeApprovals{}
	store.sites[siteID] = &site.Site{
		ID: siteID, Name: "marketing", OwnerEmail: owner.Email, MainEnvironment: site.EnvDev,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: "stage"}, {Name: envProd, RequiresApproval: true}},
	}
	return New(&sitesvc.Service{Store: store, Objects: newObjects(), Bucket: "portal", Approvals: approvals, PublicScheme: "https"}), store, approvals
}

func released(store *memStore, n int, env string) {
	r := site.Release{ID: "srel_" + strconv.Itoa(n), SiteID: siteID, Number: n, ManifestSHA256: "digest-" + strconv.Itoa(n)}
	store.releases[siteID] = append([]site.Release{r}, store.releases[siteID]...)
	if env != "" {
		e := store.sites[siteID].Environment(env)
		store.deployments[siteID] = append([]site.Deployment{{Environment: env, Action: site.ActionPublish, From: e.Release, To: n}}, store.deployments[siteID]...)
		e.Release = n
	}
}

func grant(store *memStore, email, level string) {
	if store.grants[siteID] == nil {
		store.grants[siteID] = map[string]site.Grant{}
	}
	store.grants[siteID][email] = site.Grant{Email: email, Level: level}
}

// TestSetStatus_OfflineAndOnline pins #2631: anyone who manages the site
// takes an environment offline at once, even one that requires approval, the
// release pointer is untouched, and History records both moves.
func TestSetStatus_OfflineAndOnline(t *testing.T) {
	svc, store, approvals := fixture(t)
	released(store, 1, envProd)
	grant(store, manager.Email, site.LevelManage)
	back := time.Date(2030, 1, 2, 14, 0, 0, 0, time.UTC)
	ctx := context.Background()

	res, err := svc.SetStatus(ctx, manager, "marketing", envProd, StatusChange{Status: site.StatusOffline, Message: " Back at 2pm\u202e ", BackAt: &back})
	require.NoError(t, err)
	assert.Equal(t, StatusDone, res.Status)
	env := res.Site.Environment(envProd)
	assert.Equal(t, site.StatusOffline, env.Status)
	assert.Equal(t, "Back at 2pm", env.StatusMessage)
	assert.Equal(t, &back, env.BackAt)
	assert.Equal(t, 1, env.Release, "the release pointer is untouched")
	assert.Empty(t, approvals.envSubmitted, "an outage never waits on Approvals")

	res, err = svc.SetStatus(ctx, manager, "marketing", envProd, StatusChange{Status: site.StatusOnline})
	require.NoError(t, err)
	assert.Equal(t, site.StatusOnline, res.Site.Environment(envProd).Status)
	actions := []string{store.deployments[siteID][0].Action, store.deployments[siteID][1].Action}
	assert.Equal(t, []string{site.ActionOnline, site.ActionOffline}, actions)
	assert.Equal(t, "Back at 2pm", store.deployments[siteID][1].Detail)
}

// TestSetStatus_Refusals: dev is always online, a stranger and a viewer are
// refused, an unknown status and a long message are refused.
func TestSetStatus_Refusals(t *testing.T) {
	svc, store, _ := fixture(t)
	grant(store, viewer.Email, site.LevelView)
	ctx := context.Background()
	var r *sitesvc.RefusedError
	_, err := svc.SetStatus(ctx, owner, "marketing", site.EnvDev, StatusChange{Status: site.StatusOffline})
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "always online")
	_, err = svc.SetStatus(ctx, owner, "marketing", envProd, StatusChange{Status: "paused"})
	require.ErrorAs(t, err, &r)
	_, err = svc.SetStatus(ctx, owner, "marketing", "nope", StatusChange{Status: site.StatusOffline})
	require.ErrorIs(t, err, site.ErrNoEnvironment)
	for _, a := range []sitesvc.Actor{stranger, viewer} {
		_, err = svc.SetStatus(ctx, a, "marketing", envProd, StatusChange{Status: site.StatusOffline})
		require.ErrorIs(t, err, site.ErrNotAllowed, a.Email)
	}
	_, err = svc.SetStatus(ctx, owner, "marketing", "stage", StatusChange{Status: site.StatusRedirect, RedirectTo: "http://x.example"})
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "https://")
}

// TestSetStatus_RedirectWaitsOnApprovalForANonAdministrator pins #2632: on an
// environment that requires approval a non-administrator's redirect waits,
// naming the target, and goes through once approved; an administrator's
// applies at once; a redirect may not point at the environment's own domain.
func TestSetStatus_RedirectWaitsOnApprovalForANonAdministrator(t *testing.T) {
	svc, store, approvals := fixture(t)
	svc.Config = siteconfig.Config{PagesDomain: "pages.example.net"}
	store.domains["www.client.example"] = &site.Domain{Hostname: "www.client.example", SiteID: siteID, Environment: envProd}
	ctx := context.Background()
	want := StatusChange{Status: site.StatusRedirect, RedirectTo: "https://status.example.com", KeepPath: true, Reason: "outage"}

	res, err := svc.SetStatus(ctx, owner, "marketing", envProd, want)
	require.NoError(t, err)
	assert.Equal(t, StatusAwaitingApproval, res.Status)
	require.Len(t, approvals.envSubmitted, 1)
	assert.Equal(t, approval.PurposeSiteRedirect, approvals.envSubmitted[0].Purpose)
	assert.Equal(t, envProd, approvals.envSubmitted[0].Target)
	assert.Contains(t, approvals.envSubmitted[0].Stamp, "https://status.example.com")
	assert.True(t, store.sites[siteID].Environment(envProd).Online(), "prod keeps serving while it waits")

	approvals.envApproved = true
	res, err = svc.SetStatus(ctx, owner, "marketing", envProd, want)
	require.NoError(t, err)
	assert.Equal(t, StatusDone, res.Status)
	assert.Equal(t, site.StatusRedirect, statusOf(store, envProd))

	approvals.envApproved = false
	_, err = svc.SetStatus(ctx, admin, "marketing", "stage", want)
	require.NoError(t, err)
	assert.Equal(t, site.StatusRedirect, statusOf(store, "stage"), "an environment with no approval rule redirects at once")
	_, err = svc.SetStatus(ctx, admin, "marketing", envProd, StatusChange{Status: site.StatusRedirect, RedirectTo: "https://other.example"})
	require.NoError(t, err, "an administrator's redirect applies at once")

	var r *sitesvc.RefusedError
	for _, own := range []string{"https://www.client.example/", "https://marketing.pages.example.net"} {
		_, err = svc.SetStatus(ctx, admin, "marketing", envProd, StatusChange{Status: site.StatusRedirect, RedirectTo: own})
		require.ErrorAs(t, err, &r, own)
		assert.Contains(t, r.Msg, "loop", own)
	}
}

func statusOf(store *memStore, env string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sites[siteID].Environment(env).Status
}

// TestRenameEnvironment pins #2630's rename rules and what moves with it.
func TestRenameEnvironment(t *testing.T) {
	svc, store, _ := fixture(t)
	store.sites[siteID].Environments = append(store.sites[siteID].Environments[:2], site.Environment{Name: "uat"}, store.sites[siteID].Environments[2])
	store.domains["uat.client.example"] = &site.Domain{Hostname: "uat.client.example", SiteID: siteID, Environment: "uat"}
	store.grants[siteID] = map[string]site.Grant{"pat@client.example": {Email: "pat@client.example", Level: site.LevelView, Environments: []string{"uat"}}}
	ctx := context.Background()

	st, err := svc.RenameEnvironment(ctx, owner, "marketing", "uat", "qa")
	require.NoError(t, err)
	assert.Equal(t, []string{site.EnvDev, "stage", "qa", envProd}, st.Order())
	assert.Equal(t, "qa", store.domains["uat.client.example"].Environment)
	assert.Equal(t, []string{"qa"}, store.grants[siteID]["pat@client.example"].Environments)

	var r *sitesvc.RefusedError
	for _, tc := range []struct{ from, to, want string }{
		{site.EnvDev, "x", "cannot be renamed"},
		{envProd, "x", "cannot be renamed"},
		{"qa", site.EnvDev, "the platform keeps"},
		{"qa", envProd, "the platform keeps"},
		{"qa", site.EnvAlias, "the platform keeps"},
		{"qa", "stage", "already has an environment"},
		{"qa", "bad--name", "environment name"},
		{"qa", "a23456789012345678901234567890123", "at most 32"},
	} {
		_, err := svc.RenameEnvironment(ctx, owner, "marketing", tc.from, tc.to)
		require.ErrorAs(t, err, &r, tc.to)
		assert.Contains(t, r.Msg, tc.want, tc.to)
	}
	_, err = svc.RenameEnvironment(ctx, stranger, "marketing", "qa", "review")
	require.ErrorIs(t, err, site.ErrNotAllowed)
}

// TestRemoveEnvironment pins #2630's removal rules: refused with a domain or
// the main address, an administrator's where it requires approval, and a
// grant that named only it is deleted rather than widened.
func TestRemoveEnvironment(t *testing.T) {
	svc, store, _ := fixture(t)
	store.sites[siteID].Environments = append(store.sites[siteID].Environments[:2],
		site.Environment{Name: "qa"}, site.Environment{Name: "uat", RequiresApproval: true}, store.sites[siteID].Environments[2])
	store.grants[siteID] = map[string]site.Grant{
		"only@client.example": {Email: "only@client.example", Level: site.LevelView, Environments: []string{"stage"}},
		"both@client.example": {Email: "both@client.example", Level: site.LevelView, Environments: []string{"stage", "qa"}},
	}
	ctx := context.Background()

	st, err := svc.RemoveEnvironment(ctx, owner, "marketing", "stage")
	require.NoError(t, err)
	assert.Nil(t, st.Environment("stage"))
	_, kept := store.grants[siteID]["only@client.example"]
	assert.False(t, kept, "a grant that named only the environment is deleted, never left empty")
	assert.Equal(t, []string{"qa"}, store.grants[siteID]["both@client.example"].Environments)

	var r *sitesvc.RefusedError
	_, err = svc.RemoveEnvironment(ctx, owner, "marketing", site.EnvDev)
	require.ErrorAs(t, err, &r)
	_, err = svc.RemoveEnvironment(ctx, owner, "marketing", envProd)
	require.ErrorAs(t, err, &r)
	_, err = svc.RemoveEnvironment(ctx, owner, "marketing", "uat")
	require.ErrorIs(t, err, site.ErrNotAllowed, "only an administrator removes an environment that requires approval")
	store.domains["qa.client.example"] = &site.Domain{Hostname: "qa.client.example", SiteID: siteID, Environment: "qa"}
	_, err = svc.RemoveEnvironment(ctx, owner, "marketing", "qa")
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "has a domain")
	assert.ErrorIs(t, err, site.ErrEnvironmentInUse)
	_, err = svc.RemoveEnvironment(ctx, admin, "marketing", "uat")
	require.NoError(t, err)
}

// TestAgentMoves pins #2643 in the service: an agent's promote into an
// environment other than dev waits on the person, unless the person accepted
// that exact move, a person approved it, or an administrator lets agents move
// that environment; a person's own move is unchanged.
func TestAgentMoves(t *testing.T) {
	svc, store, approvals := fixture(t)
	released(store, 1, site.EnvDev)
	ctx := context.Background()
	agent := admin
	agent.Agent = true

	_, err := svc.Promote(ctx, agent, sitesvc.PromoteRequest{Site: "marketing", To: "stage"})
	wait, ok := errors.AsType[*PersonDecisionError](err)
	require.True(t, ok, "%v", err)
	assert.Contains(t, wait.Error(), "APPROVAL_REQUIRED")
	assert.Zero(t, store.sites[siteID].Environment("stage").Release)

	req, err := svc.RequestMove(ctx, agent, wait.Move)
	require.NoError(t, err)
	assert.Equal(t, "apr_1", req.ID)
	assert.True(t, approvals.actors[len(approvals.actors)-1].Agent, "the request says an agent asked")

	agent.Decided = wait.Move.Key()
	got, err := svc.Promote(ctx, agent, sitesvc.PromoteRequest{Site: "marketing", To: "stage"})
	require.NoError(t, err)
	assert.Equal(t, site.DecidedByElicitation, got.DecidedBy)
	assert.Equal(t, site.DecidedByElicitation, store.deployments[siteID][0].DecidedBy)

	agent.Decided = ""
	approvals.approved = true
	got, err = svc.Promote(ctx, agent, sitesvc.PromoteRequest{Site: "marketing", To: envProd})
	require.NoError(t, err)
	assert.Equal(t, site.DecidedByApproval, got.DecidedBy)
	assert.Empty(t, approvals.selfRecords, "an approved agent move records no approval of its own")

	approvals.approved = false
	store.sites[siteID].Environment("stage").AgentsMayPromote = true
	_, err = svc.Rollback(ctx, agent, "marketing", "stage", 0)
	require.Error(t, err, "stage has served no earlier release")
	released(store, 2, site.EnvDev)
	got, err = svc.Promote(ctx, agent, sitesvc.PromoteRequest{Site: "marketing", To: "stage"})
	require.NoError(t, err)
	assert.Equal(t, site.DecidedByEnvironment, got.DecidedBy)

	got, err = svc.Promote(ctx, admin, sitesvc.PromoteRequest{Site: "marketing", To: envProd})
	require.NoError(t, err)
	assert.Empty(t, got.DecidedBy, "a person's own move is theirs")
}

// TestAgentsMayPromoteIsAnAdministratorsInThePortal pins #2643's setting.
func TestAgentsMayPromoteIsAnAdministratorsInThePortal(t *testing.T) {
	svc, store, _ := fixture(t)
	ctx := context.Background()
	yes := true
	agent := admin
	agent.Agent = true
	var r *sitesvc.RefusedError
	_, err := svc.SetEnvironment(ctx, agent, "marketing", "stage", sitesvc.EnvironmentChange{AgentsMayPromote: &yes})
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "never from an agent's session")
	_, err = svc.SetEnvironment(ctx, owner, "marketing", "stage", sitesvc.EnvironmentChange{AgentsMayPromote: &yes})
	require.ErrorIs(t, err, site.ErrNotAllowed)
	_, err = svc.SetEnvironment(ctx, admin, "marketing", site.EnvDev, sitesvc.EnvironmentChange{AgentsMayPromote: &yes})
	require.ErrorAs(t, err, &r)
	_, err = svc.SetEnvironment(ctx, admin, "marketing", "stage", sitesvc.EnvironmentChange{AgentsMayPromote: &yes})
	require.NoError(t, err)
	assert.True(t, store.sites[siteID].Environment("stage").AgentsMayPromote)
}
