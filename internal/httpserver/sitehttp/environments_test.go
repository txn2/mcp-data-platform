package sitehttp

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
)

func (m *memStore) SetStatus(_ context.Context, siteID, name string, s site.EnvironmentStatus, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sites[siteID]
	if !ok {
		return site.ErrNotFound
	}
	env := st.Environment(name)
	if env == nil {
		return site.ErrNoEnvironment
	}
	now := time.Now()
	env.Status, env.StatusMessage, env.BackAt, env.RedirectTo, env.RedirectKeepPath = s.Status, s.Message, s.BackAt, s.RedirectTo, s.KeepPath
	env.StatusChangedBy, env.StatusChangedAt = actor, &now
	return nil
}

func (m *memStore) RenameEnvironment(_ context.Context, siteID, from, to, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sites[siteID]
	if !ok {
		return site.ErrNotFound
	}
	if st.Environment(to) != nil {
		return &site.InUseError{Msg: "an environment named " + to + " exists"}
	}
	env := st.Environment(from)
	if env == nil {
		return site.ErrNoEnvironment
	}
	env.Name, env.UpdatedBy = to, actor
	return nil
}

func (m *memStore) RemoveEnvironment(_ context.Context, siteID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sites[siteID]
	if !ok {
		return site.ErrNotFound
	}
	if st.MainEnvironment == name {
		return &site.InUseError{Msg: name + " holds the main address"}
	}
	i := slices.IndexFunc(st.Environments, func(e site.Environment) bool { return e.Name == name })
	if i < 0 {
		return site.ErrNoEnvironment
	}
	st.Environments = slices.Delete(st.Environments, i, i+1)
	return nil
}

// TestPortal_EnvironmentStatus pins #2631 and #2632 on the portal's route: an
// owner takes an environment offline and brings it back, a redirect to an
// environment that requires approval waits for one, and each is audited as
// manage_site's environment command.
func TestPortal_EnvironmentStatus(t *testing.T) {
	f := newFixture(t)
	path := siteBase + "/environments/stage"

	off := decodeBody[EnvironmentResponse](t, f.statusIs(t, http.StatusOK, route{http.MethodPut, path, map[string]any{"status": "offline", "message": "Back at 2pm"}}, userOwner))
	assert.Equal(t, sitesvc.StatusDone, off.Status)
	assert.Equal(t, site.StatusOffline, f.store.sites[siteID].Environment("stage").Status)
	assert.Equal(t, "Back at 2pm", f.store.sites[siteID].Environment("stage").StatusMessage)
	assert.Equal(t, sitetool.CmdEnvironment, f.audit.last().Parameters["command"])
	assert.Equal(t, "offline", f.audit.last().Parameters["status"])

	f.statusIs(t, http.StatusOK, route{http.MethodPut, path, map[string]any{"status": "online"}}, userOwner)
	assert.Equal(t, site.StatusOnline, f.store.sites[siteID].Environment("stage").Status)

	waiting := decodeBody[EnvironmentResponse](t, f.statusIs(t, http.StatusOK, route{http.MethodPut, siteBase + "/environments/" + envProd, map[string]any{"status": "redirect", "redirect_to": "https://status.example.com"}}, userManager))
	assert.Equal(t, sitesvc.StatusAwaitingApproval, waiting.Status)
	require.NotNil(t, waiting.Approval)
	assert.Empty(t, f.store.sites[siteID].Environment(envProd).Status, "a redirect waiting for approval changes nothing yet")

	f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPut, path, map[string]any{"status": "redirect", "redirect_to": "http://status.example.com"}}, userOwner)
	f.statusIs(t, http.StatusForbidden, route{http.MethodPut, path, map[string]any{"status": "offline"}}, userViewer)
}

// TestPortal_RenameAndRemoveEnvironment pins #2630 on the portal's routes.
func TestPortal_RenameAndRemoveEnvironment(t *testing.T) {
	f := newFixture(t)

	renamed := decodeBody[EnvironmentResponse](t, f.statusIs(t, http.StatusOK, route{http.MethodPut, siteBase + "/environments/stage", map[string]any{"rename_to": "qa"}}, userOwner))
	assert.NotNil(t, renamed.Site.Environment("qa"))
	assert.Nil(t, renamed.Site.Environment("stage"))
	assert.Equal(t, "qa", f.audit.last().Parameters["rename_to"])

	rec := f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPut, siteBase + "/environments/qa", map[string]any{"rename_to": "uat", "status": "offline"}}, userOwner)
	assert.Contains(t, rec.Body.String(), "rename_to renames the environment and nothing else")
	f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPut, siteBase + "/environments/" + envProd, map[string]any{"rename_to": "live"}}, userOwner)

	f.statusIs(t, http.StatusForbidden, route{http.MethodDelete, siteBase + "/environments/qa", nil}, userViewer)
	view := decodeBody[SiteView](t, f.statusIs(t, http.StatusOK, route{http.MethodDelete, siteBase + "/environments/qa", nil}, userOwner))
	assert.Nil(t, view.Environment("qa"))
	assert.Equal(t, sitetool.CmdRemoveEnvironment, f.audit.last().Parameters["command"])
	f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodDelete, siteBase + "/environments/" + site.EnvDev, nil}, userOwner)
	f.statusIs(t, http.StatusNotFound, route{http.MethodDelete, siteBase + "/environments/nope", nil}, userOwner)
}
