package sitehttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siterouting"
)

// fakeRouting is the integrations' store with a Site routing script chosen
// whose last run failed www.client.example.
type fakeRouting struct {
	integration.Bindings
	retried   int
	startedAt time.Time
}

func (f *fakeRouting) Binding(context.Context, string) (*integration.Binding, error) {
	b := &integration.Binding{Operation: integration.OpSiteRouting, LastRunID: "run1", LastError: "upstream 503"}
	if !f.startedAt.IsZero() {
		b.LastStartedAt = &f.startedAt
	}
	return b, nil
}

func (*fakeRouting) ItemsByKey(context.Context, string, []string) (map[string]integration.StoredItem, error) {
	return map[string]integration.StoredItem{"www.client.example": {
		Key: "www.client.example", Status: "failed", Message: "The site routing script failed", FailedRun: "run1",
	}}, nil
}

func (f *fakeRouting) RequestRetry(context.Context, string) error { f.retried++; return nil }

// TestPortal_Routing pins #2659 on the portal API: with a script chosen, a
// site's domains and pages addresses carry their routing status, the run
// error only for an administrator; anyone who can see the site may retry;
// with none chosen nothing is added and retry is 404.
func TestPortal_Routing(t *testing.T) {
	f := newFixture(t)
	f.store.domains["www.client.example"] = &site.Domain{Hostname: "www.client.example", SiteID: siteID, Environment: envProd}

	plain := f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userOwner).Body.String()
	assert.NotContains(t, plain, `"routing"`, "with no script chosen the field is omitted")
	assert.NotContains(t, plain, `"pages_routing"`)
	f.statusIs(t, http.StatusNotFound, route{http.MethodPost, siteBase + "/routing/retry", nil}, userOwner)

	r := &fakeRouting{}
	f.rt.Routing = &siterouting.Lookup{Service: &integration.Service{Store: r}, Addressing: f.rt.Service.Addressing}
	owner := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userOwner))
	require.Len(t, owner.Domains, 1)
	require.NotNil(t, owner.Domains[0].Routing)
	assert.Equal(t, "The site routing script failed", owner.Domains[0].Routing.Message)
	assert.Empty(t, owner.Domains[0].Routing.RunError, "an owner never reads the run error")
	assert.NotEmpty(t, owner.PagesRouting)
	for _, st := range owner.PagesRouting {
		assert.Equal(t, "pending", st.Status)
	}

	admin := decodeBody[SiteView](t, f.statusIs(t, 200, route{http.MethodGet, siteBase, nil}, userAdmin))
	assert.Equal(t, "upstream 503", admin.Domains[0].Routing.RunError)

	list := f.statusIs(t, 200, route{http.MethodGet, base, nil}, userOwner).Body.String()
	assert.NotContains(t, list, `"routing"`, "the Sites list reads no routing")

	f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/routing/retry", nil}, userViewer)
	assert.Equal(t, 1, r.retried)
	f.statusIs(t, http.StatusNotFound, route{http.MethodPost, base + "/nope/routing/retry", nil}, userOwner)

	// A reader's retry needs a failed hostname of the site, and waits out the
	// interval after a run starts; an administrator's needs neither.
	delete(f.store.domains, "www.client.example")
	f.statusIs(t, http.StatusConflict, route{http.MethodPost, siteBase + "/routing/retry", nil}, userViewer)
	f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/routing/retry", nil}, userAdmin)
	f.store.domains["www.client.example"] = &site.Domain{Hostname: "www.client.example", SiteID: siteID, Environment: envProd}
	r.startedAt = time.Now()
	soon := f.statusIs(t, http.StatusTooManyRequests, route{http.MethodPost, siteBase + "/routing/retry", nil}, userViewer).Body.String()
	assert.Contains(t, soon, "ask again in")
	assert.Equal(t, 2, r.retried)
}
